package helper

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/matrix-org/gomatrix"
	"github.com/sirupsen/logrus"
)

type Config struct {
	HomeserverURL string
	Username      string
	Password      string // #nosec G117 -- used only for login, never serialized
	Domain        string
	DeviceID      string
}

type Room struct {
	ID                string
	Name              string
	Topic             string
	LastActivity      int64
	LastEventID       string
	NotificationCount int
}

type Notification struct {
	RoomID string
	Event  gomatrix.Event
	TS     int64
}

type Client interface {
	GetRoomsViaSync(ctx context.Context) ([]Room, error)
	GetNotifications(ctx context.Context, since int64) ([]Notification, error)
	LeaveRoom(ctx context.Context, roomID, roomName string) error

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

// syncResponse is a custom sync response struct that captures unread_notifications
// which gomatrix's RespSync does not support.
type syncResponse struct {
	Rooms struct {
		Join map[string]syncJoinedRoom `json:"join"`
	} `json:"rooms"`
}

type syncJoinedRoom struct {
	State struct {
		Events []gomatrix.Event `json:"events"`
	} `json:"state"`
	Timeline struct {
		Events []gomatrix.Event `json:"events"`
	} `json:"timeline"`
	UnreadNotifications struct {
		NotificationCount int `json:"notification_count"`
		HighlightCount    int `json:"highlight_count"`
	} `json:"unread_notifications"`
}

func (c *matrixClient) GetRoomsViaSync(ctx context.Context) ([]Room, error) {
	var resp syncResponse
	err := c.retry.Do(ctx, func() error {
		query := map[string]string{
			"timeout":      "0",
			"filter":       syncFilter,
			"full_state":   "true",
			"set_presence": "offline",
		}
		urlPath := c.client.BuildURLWithQuery([]string{"sync"}, query)
		return c.client.MakeRequest("GET", urlPath, nil, &resp)
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

		room.NotificationCount = joinedRoom.UnreadNotifications.NotificationCount
		rooms = append(rooms, room)
	}

	return rooms, nil
}

type notificationsResponse struct {
	Notifications []struct {
		Event  gomatrix.Event `json:"event"`
		RoomID string         `json:"room_id"`
		TS     int64          `json:"ts"`
	} `json:"notifications"`
	NextToken string `json:"next_token"`
}

func (c *matrixClient) GetNotifications(ctx context.Context, since int64) ([]Notification, error) {
	var all []Notification
	from := ""

	for {
		var resp notificationsResponse
		currentFrom := from
		err := c.retry.Do(ctx, func() error {
			query := map[string]string{
				"limit": "100",
				"only":  "highlight",
			}
			if currentFrom != "" {
				query["from"] = currentFrom
			}
			urlPath := c.client.BuildURLWithQuery([]string{"notifications"}, query)
			return c.client.MakeRequest("GET", urlPath, nil, &resp)
		})
		if err != nil {
			return all, Wrap(err, "failed to get notifications")
		}

		if len(resp.Notifications) == 0 {
			break
		}

		reachedEnd := false
		for _, n := range resp.Notifications {
			if n.TS < since {
				reachedEnd = true
				break
			}
			all = append(all, Notification{
				RoomID: n.RoomID,
				Event:  n.Event,
				TS:     n.TS,
			})
		}

		if reachedEnd || resp.NextToken == "" {
			break
		}
		from = resp.NextToken
	}

	return all, nil
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
		return nil
	})

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

