package helper

import (
	"context"
	"fmt"
	"strings"

	"github.com/matrix-org/gomatrix"
	"github.com/sirupsen/logrus"
)

type Config struct {
	HomeserverURL string
	Username      string
	Password      string
	Domain        string
}

type Message struct {
	ID        string
	Sender    string
	Content   map[string]interface{}
	Type      string
	Timestamp int64
}

type Room struct {
	ID          string
	Name        string
	Topic       string
	MemberCount int
	JoinedAt    int64
}

type Client interface {
	GetRooms(ctx context.Context) ([]Room, error)
	LeaveRoom(ctx context.Context, roomID string) error

	GetMessages(ctx context.Context, roomID string, since int64) ([]Message, error)
	GetLastMessageTimestamp(ctx context.Context, roomID string) (int64, error)
	MarkRoomAsRead(ctx context.Context, roomID string) error

	Config() Config
	UserID() string
}

type matrixClient struct {
	client    *gomatrix.Client
	log       *logrus.Logger
	config    Config
	userID    string
	retry     *Retry
	retryOpts RetryOptions
	cbOpts    CircuitBreakerOptions
	rlOpts    RateLimiterOptions
}

type ClientOption func(*matrixClient)

func WithRetryOptions(opts RetryOptions) ClientOption {
	return func(mc *matrixClient) {
		mc.retryOpts = opts
	}
}

func WithCircuitBreakerOptions(opts CircuitBreakerOptions) ClientOption {
	return func(mc *matrixClient) {
		mc.cbOpts = opts
	}
}

func WithRateLimiterOptions(opts RateLimiterOptions) ClientOption {
	return func(mc *matrixClient) {
		mc.rlOpts = opts
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

	mc := &matrixClient{
		client:    client,
		log:       log,
		config:    cfg,
		retryOpts: DefaultRetryOptions,
		cbOpts:    DefaultCircuitBreakerOptions,
		rlOpts:    DefaultRateLimiterOptions,
	}

	for _, opt := range opts {
		opt(mc)
	}

	mc.retry = NewRetry(mc.retryOpts, mc.cbOpts, mc.rlOpts)

	err = mc.retry.Do(context.Background(), func() error {
		resp, err := client.Login(&gomatrix.ReqLogin{
			Type:     "m.login.password",
			User:     cfg.Username,
			Password: cfg.Password,
		})
		if err != nil {
			return Wrap(err, "failed to login")
		}

		client.SetCredentials(resp.UserID, resp.AccessToken)
		mc.userID = resp.UserID
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

func (c *matrixClient) GetRooms(ctx context.Context) ([]Room, error) {
	var rooms []Room

	err := c.retry.Do(ctx, func() error {
		resp, err := c.client.JoinedRooms()
		if err != nil {
			return Wrap(err, "failed to get joined rooms")
		}

		rooms = make([]Room, 0, len(resp.JoinedRooms))
		for _, roomID := range resp.JoinedRooms {
			room, err := c.getRoomInfo(roomID)
			if err != nil {
				c.log.WithError(err).WithField("room_id", roomID).Warn("Failed to get room info")
				rooms = append(rooms, Room{
					ID:   roomID,
					Name: roomID,
				})
				continue
			}
			rooms = append(rooms, room)
		}
		return nil
	})

	if err != nil {
		return nil, Wrap(err, "failed to get rooms after retries")
	}

	return rooms, nil
}

func (c *matrixClient) getRoomInfo(roomID string) (Room, error) {
	room := Room{
		ID: roomID,
	}

	var nameContent struct {
		Name string `json:"name"`
	}
	err := c.client.StateEvent(roomID, "m.room.name", "", &nameContent)
	if err == nil && nameContent.Name != "" {
		room.Name = nameContent.Name
	} else {
		room.Name = roomID
	}

	var topicContent struct {
		Topic string `json:"topic"`
	}
	err = c.client.StateEvent(roomID, "m.room.topic", "", &topicContent)
	if err == nil {
		room.Topic = topicContent.Topic
	}

	return room, nil
}

func (c *matrixClient) getRoomNameSafe(roomID string) string {
	var nameContent struct {
		Name string `json:"name"`
	}
	err := c.client.StateEvent(roomID, "m.room.name", "", &nameContent)

	if err == nil && nameContent.Name != "" {
		return nameContent.Name
	}

	var summaryContent struct {
		Heroes      []string `json:"m.heroes"`
		Invitecount int      `json:"m.invited_member_count"`
		Joincount   int      `json:"m.joined_member_count"`
	}

	err = c.client.StateEvent(roomID, "m.room.summary", "", &summaryContent)
	if err == nil && len(summaryContent.Heroes) > 0 {
		return "Direct chat with " + strings.Join(summaryContent.Heroes, ", ")
	}

	return roomID
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

		c.log.WithError(err).WithFields(logrus.Fields{
			"room_id":   roomID,
			"room_name": c.getRoomNameSafe(roomID),
		}).Debug("Failed to get last message timestamp")

		return 0, Wrap(err, "failed to get last message timestamp")
	}

	return timestamp, nil
}

func (c *matrixClient) LeaveRoom(ctx context.Context, roomID string) error {
	roomName := c.getRoomNameSafe(roomID)
	noRLRetry := NewRetry(
		c.retryOpts,
		c.cbOpts,
		RateLimiterOptions{Enabled: false},
	)
	err := noRLRetry.Do(ctx, func() error {
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
	})

	return err
}

func (c *matrixClient) MarkRoomAsRead(ctx context.Context, roomID string) error {
	roomName := c.getRoomNameSafe(roomID)

	noRLRetry := NewRetry(
		c.retryOpts,
		c.cbOpts,
		RateLimiterOptions{Enabled: false},
	)
	err := noRLRetry.Do(ctx, func() error {
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

		err = c.client.MakeRequest(
			"POST",
			c.client.BuildURL("rooms", roomID, "receipt", "m.read", resp.Chunk[0].ID),
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
	})

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

func (m *Message) IsFromUser(userID string) bool {
	return m.Sender == userID
}

func (m *Message) GetBody() string {
	body, ok := m.Content["body"].(string)
	if !ok {
		return ""
	}
	return body
}
