package helper

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/matrix-org/gomatrix"
	"github.com/sirupsen/logrus"
)

type Config struct {
	HomeserverURL string
	Username      string
	Password      string
	Domain        string
	DeviceID      string
}

type Message struct {
	ID        string
	Sender    string
	Content   map[string]interface{}
	Type      string
	Timestamp int64
}

type Room struct {
	ID           string
	Name         string
	Topic        string
	LastActivity int64
	LastEventID  string
}

type RoomWithTimeline struct {
	Room
	TimelineEvents  []Message
	TimelineLimited bool
}

type Client interface {
	GetRoomsViaSync(ctx context.Context) ([]Room, error)
	GetRoomsWithTimeline(ctx context.Context, timelineLimit int) ([]RoomWithTimeline, error)
	LeaveRoom(ctx context.Context, roomID, roomName string) error

	GetMessages(ctx context.Context, roomID string, since int64) ([]Message, error)
	GetLastMessageTimestamp(ctx context.Context, roomID string) (int64, error)
	MarkRoomAsRead(ctx context.Context, roomID, roomName, lastEventID string) error

	Config() Config
	UserID() string
	AccessToken() string
	DeviceID() string
}

type matrixClient struct {
	client      *gomatrix.Client
	log         *logrus.Logger
	config      Config
	userID      string
	accessToken string
	deviceID    string
	retry       *Retry
}

type clientConfig struct {
	retryOpts RetryOptions
	cbOpts    CircuitBreakerOptions
	rlOpts    RateLimiterOptions
}

type ClientOption func(*clientConfig)

func WithRetryOptions(opts RetryOptions) ClientOption {
	return func(cc *clientConfig) {
		cc.retryOpts = opts
	}
}

func WithCircuitBreakerOptions(opts CircuitBreakerOptions) ClientOption {
	return func(cc *clientConfig) {
		cc.cbOpts = opts
	}
}

func WithRateLimiterOptions(opts RateLimiterOptions) ClientOption {
	return func(cc *clientConfig) {
		cc.rlOpts = opts
	}
}

func NewClient(cfg Config, log *logrus.Logger, opts ...ClientOption) (Client, error) {

	if cfg.HomeserverURL == "" {
		return nil, fmt.Errorf("homeserver URL is required")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, fmt.Errorf("username and password are required")
	}

	client, err := gomatrix.NewClient(cfg.HomeserverURL, "", "")
	if err != nil {
		return nil, Wrap(err, "failed to create matrix client")
	}

	cc := &clientConfig{
		retryOpts: DefaultRetryOptions,
		cbOpts:    DefaultCircuitBreakerOptions,
		rlOpts:    DefaultRateLimiterOptions,
	}
	for _, opt := range opts {
		opt(cc)
	}

	client.Client = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
		Timeout: 2 * time.Minute,
	}

	mc := &matrixClient{
		client: client,
		log:    log,
		config: cfg,
		retry:  NewRetry(cc.retryOpts, cc.cbOpts, cc.rlOpts),
	}

	err = mc.retry.Do(context.Background(), func() error {
		resp, err := client.Login(&gomatrix.ReqLogin{
			Type:                     "m.login.password",
			User:                     cfg.Username,
			Password:                 cfg.Password,
			DeviceID:                 cfg.DeviceID,
			InitialDeviceDisplayName: "matrix-helper",
		})
		if err != nil {
			return Wrap(err, "failed to login")
		}

		client.SetCredentials(resp.UserID, resp.AccessToken)
		mc.userID = resp.UserID
		mc.accessToken = resp.AccessToken
		mc.deviceID = resp.DeviceID
		return nil
	})

	if err != nil {
		return nil, Wrap(err, "failed to authenticate after retries")
	}

	return mc, nil
}

func (c *matrixClient) Config() Config {
	return c.config
}

func (c *matrixClient) UserID() string {
	return c.userID
}

func (c *matrixClient) AccessToken() string {
	return c.accessToken
}

func (c *matrixClient) DeviceID() string {
	return c.deviceID
}

type whoamiResponse struct {
	UserID string `json:"user_id"`
}

func NewClientFromToken(token *CachedToken, log *logrus.Logger, opts ...ClientOption) (Client, error) {
	client, err := gomatrix.NewClient(token.HomeserverURL, token.UserID, token.AccessToken)
	if err != nil {
		return nil, Wrap(err, "failed to create matrix client from token")
	}

	cc := &clientConfig{
		retryOpts: DefaultRetryOptions,
		cbOpts:    DefaultCircuitBreakerOptions,
		rlOpts:    DefaultRateLimiterOptions,
	}
	for _, opt := range opts {
		opt(cc)
	}

	client.Client = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
		Timeout: 2 * time.Minute,
	}

	var resp whoamiResponse
	err = client.MakeRequest(
		"GET",
		client.BuildURL("account", "whoami"),
		nil,
		&resp,
	)
	if err != nil {
		return nil, Wrap(err, "cached token is invalid")
	}
	if resp.UserID != token.UserID {
		return nil, fmt.Errorf("token user_id mismatch: expected %s, got %s", token.UserID, resp.UserID)
	}

	mc := &matrixClient{
		client: client,
		log:    log,
		config: Config{
			HomeserverURL: token.HomeserverURL,
			Username:      token.Username,
			Domain:        token.Domain,
			DeviceID:      token.DeviceID,
		},
		userID:      token.UserID,
		accessToken: token.AccessToken,
		deviceID:    token.DeviceID,
		retry:       NewRetry(cc.retryOpts, cc.cbOpts, cc.rlOpts),
	}

	return mc, nil
}

func LogoutAndCleanup(token *CachedToken) error {
	client, err := gomatrix.NewClient(token.HomeserverURL, token.UserID, token.AccessToken)
	if err != nil {
		_ = RemoveToken()
		return Wrap(err, "failed to create client for logout")
	}

	_, err = client.Logout()
	_ = RemoveToken()
	if err != nil {
		return Wrap(err, "failed to logout from server")
	}
	return nil
}

const syncFilter = `{"presence":{"types":[]},"account_data":{"types":[]},"room":{"state":{"types":["m.room.name","m.room.topic"]},"timeline":{"limit":1},"ephemeral":{"types":[]},"account_data":{"types":[]}}}`

func (c *matrixClient) GetRoomsViaSync(ctx context.Context) ([]Room, error) {
	var resp *gomatrix.RespSync
	err := c.retry.Do(ctx, func() error {
		var err error
		resp, err = c.client.SyncRequest(0, "", syncFilter, true, "offline")
		if err != nil {
			return Wrap(err, "failed to sync")
		}
		return nil
	})
	if err != nil {
		return nil, Wrap(err, "failed to sync after retries")
	}

	rooms := make([]Room, 0, len(resp.Rooms.Join))
	for roomID, joinedRoom := range resp.Rooms.Join {
		room := Room{ID: roomID, Name: roomID}

		for _, ev := range joinedRoom.State.Events {
			switch ev.Type {
			case "m.room.name":
				if name, ok := ev.Content["name"].(string); ok && name != "" {
					room.Name = name
				}
			case "m.room.topic":
				if topic, ok := ev.Content["topic"].(string); ok {
					room.Topic = topic
				}
			}
		}

		if len(joinedRoom.Timeline.Events) > 0 {
			last := joinedRoom.Timeline.Events[len(joinedRoom.Timeline.Events)-1]
			room.LastActivity = last.Timestamp
			room.LastEventID = last.ID
		}

		rooms = append(rooms, room)
	}

	return rooms, nil
}

func (c *matrixClient) GetRoomsWithTimeline(ctx context.Context, timelineLimit int) ([]RoomWithTimeline, error) {
	filter := fmt.Sprintf(
		`{"presence":{"types":[]},"account_data":{"types":[]},"room":{"state":{"types":["m.room.name","m.room.topic"]},"timeline":{"limit":%d},"ephemeral":{"types":[]},"account_data":{"types":[]}}}`,
		timelineLimit,
	)

	var resp *gomatrix.RespSync
	err := c.retry.Do(ctx, func() error {
		var err error
		resp, err = c.client.SyncRequest(0, "", filter, true, "offline")
		if err != nil {
			return Wrap(err, "failed to sync")
		}
		return nil
	})
	if err != nil {
		return nil, Wrap(err, "failed to sync after retries")
	}

	rooms := make([]RoomWithTimeline, 0, len(resp.Rooms.Join))
	for roomID, joinedRoom := range resp.Rooms.Join {
		room := RoomWithTimeline{
			Room:            Room{ID: roomID, Name: roomID},
			TimelineLimited: joinedRoom.Timeline.Limited,
		}

		for _, ev := range joinedRoom.State.Events {
			switch ev.Type {
			case "m.room.name":
				if name, ok := ev.Content["name"].(string); ok && name != "" {
					room.Name = name
				}
			case "m.room.topic":
				if topic, ok := ev.Content["topic"].(string); ok {
					room.Topic = topic
				}
			}
		}

		if len(joinedRoom.Timeline.Events) > 0 {
			last := joinedRoom.Timeline.Events[len(joinedRoom.Timeline.Events)-1]
			room.LastActivity = last.Timestamp
			room.LastEventID = last.ID
		}

		for _, ev := range joinedRoom.Timeline.Events {
			if ev.Type == "m.room.message" {
				room.TimelineEvents = append(room.TimelineEvents, Message{
					ID:        ev.ID,
					Sender:    ev.Sender,
					Content:   ev.Content,
					Type:      ev.Type,
					Timestamp: ev.Timestamp,
				})
			}
		}

		rooms = append(rooms, room)
	}

	return rooms, nil
}

func (c *matrixClient) GetMessages(ctx context.Context, roomID string, since int64) ([]Message, error) {
	var messages []Message

	err := c.retry.Do(ctx, func() error {

		resp, err := c.client.Messages(roomID, "", "", 'b', 100)
		if err != nil {
			return Wrap(err, "failed to get messages")
		}

		messages = make([]Message, 0, len(resp.Chunk))
		for _, event := range resp.Chunk {

			if event.Timestamp < since {
				continue
			}

			if event.Type == "m.room.message" {
				messages = append(messages, Message{
					ID:        event.ID,
					Sender:    event.Sender,
					Content:   event.Content,
					Type:      event.Type,
					Timestamp: event.Timestamp,
				})
			}
		}
		return nil
	})

	if err != nil {
		return nil, Wrap(err, "failed to get messages after retries")
	}

	return messages, nil
}

func (c *matrixClient) GetLastMessageTimestamp(ctx context.Context, roomID string) (int64, error) {
	var timestamp int64

	err := c.retry.Do(ctx, func() error {
		resp, err := c.client.Messages(roomID, "", "", 'b', 1)
		if err != nil {
			return Wrap(err, "failed to get messages")
		}

		if len(resp.Chunk) == 0 {

			return fmt.Errorf("no messages found in room %s", roomID)
		}

		timestamp = resp.Chunk[0].Timestamp
		return nil
	})

	if err != nil {

		c.log.WithError(err).WithField("room_id", roomID).Debug("Failed to get last message timestamp")

		return 0, Wrap(err, "failed to get last message timestamp")
	}

	return timestamp, nil
}

func (c *matrixClient) LeaveRoom(ctx context.Context, roomID, roomName string) error {
	err := c.retry.Do(ctx, func() error {
		_, err := c.client.LeaveRoom(roomID)
		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"room_id":   roomID,
				"room_name": roomName,
			}).Debug("Failed to leave room")
			return Wrap(err, "failed to leave room")
		}

		c.log.WithFields(logrus.Fields{
			"room_name": roomName,
		}).Info("Successfully left room")

		return nil
	}, WithoutRateLimit())

	return err
}

func (c *matrixClient) MarkRoomAsRead(ctx context.Context, roomID, roomName, lastEventID string) error {
	err := c.retry.Do(ctx, func() error {
		eventID := lastEventID
		if eventID == "" {
			resp, err := c.client.Messages(roomID, "", "", 'b', 1)
			if err != nil {
				c.log.WithError(err).WithFields(logrus.Fields{
					"room_id":   roomID,
					"room_name": roomName,
				}).Debug("Failed to get messages for marking room as read")
				return Wrap(err, "failed to get messages")
			}
			if len(resp.Chunk) == 0 {
				return nil
			}
			eventID = resp.Chunk[0].ID
		}

		err := c.client.MakeRequest(
			"POST",
			c.client.BuildURL("rooms", roomID, "receipt", "m.read", eventID),
			struct{}{},
			nil,
		)
		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"room_id":   roomID,
				"room_name": roomName,
			}).Debug("Failed to mark room as read")
			return Wrap(err, "failed to mark room as read")
		}

		c.log.WithFields(logrus.Fields{
			"room_name": roomName,
		}).Debug("Successfully marked room as read")

		return nil
	}, WithoutRateLimit())

	return err
}

func (m *Message) ContainsMention(username string) bool {
	body, ok := m.Content["body"].(string)
	if !ok {
		return false
	}
	mentionFormat := fmt.Sprintf("@%s:", username)
	return strings.Contains(body, mentionFormat)
}
