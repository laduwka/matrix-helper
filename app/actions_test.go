package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/laduwka/matrix-helper/helper"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClient implements helper.Client for testing
type mockClient struct {
	rooms             []helper.Room
	getRoomsErr       error
	leaveRoomErr      error
	leaveRoomCalls    []string
	messages          map[string][]helper.Message
	getMessagesErr    error
	lastTimestamp     map[string]int64
	lastTimestampErr  map[string]error
	markAsReadErr     error
	markAsReadCalls   []string
	config            helper.Config
	userID            string
}

func newMockClient() *mockClient {
	return &mockClient{
		messages:         make(map[string][]helper.Message),
		lastTimestamp:    make(map[string]int64),
		lastTimestampErr: make(map[string]error),
		config: helper.Config{
			Domain:   "matrix.example.com",
			Username: "testuser",
		},
		userID: "@testuser:matrix.example.com",
	}
}

func (m *mockClient) GetRooms(_ context.Context) ([]helper.Room, error) {
	if m.getRoomsErr != nil {
		return nil, m.getRoomsErr
	}
	return m.rooms, nil
}

func (m *mockClient) LeaveRoom(_ context.Context, roomID string) error {
	m.leaveRoomCalls = append(m.leaveRoomCalls, roomID)
	return m.leaveRoomErr
}

func (m *mockClient) GetMessages(_ context.Context, roomID string, _ int64) ([]helper.Message, error) {
	if m.getMessagesErr != nil {
		return nil, m.getMessagesErr
	}
	return m.messages[roomID], nil
}

func (m *mockClient) GetLastMessageTimestamp(_ context.Context, roomID string) (int64, error) {
	if err, ok := m.lastTimestampErr[roomID]; ok {
		return 0, err
	}
	if ts, ok := m.lastTimestamp[roomID]; ok {
		return ts, nil
	}
	return 0, errors.New("no messages found in room")
}

func (m *mockClient) MarkRoomAsRead(_ context.Context, roomID string) error {
	m.markAsReadCalls = append(m.markAsReadCalls, roomID)
	return m.markAsReadErr
}

func (m *mockClient) Config() helper.Config {
	return m.config
}

func (m *mockClient) UserID() string {
	return m.userID
}

func newTestActions(client helper.Client) ActionService {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return NewActions(client, log, WithConcurrency(1))
}

// FindRoomsToLeave tests

func TestFindRoomsToLeave_WithCloseKeyword_DaysZero(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Ticket closed"},
		{ID: "!room2:test", Name: "General chat"},
	}
	actions := newTestActions(mc)

	toLeave, toKeep, err := actions.FindRoomsToLeave(context.Background(), 0)
	require.NoError(t, err)

	assert.Len(t, toLeave, 1)
	assert.Equal(t, "!room1:test", toLeave[0].Room.ID)
	assert.Len(t, toKeep, 1)
	assert.Equal(t, "!room2:test", toKeep[0].ID)
}

func TestFindRoomsToLeave_InactiveWithCloseKeyword(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Issue done"},
	}
	// 60 days ago in milliseconds
	mc.lastTimestamp["!room1:test"] = time.Now().AddDate(0, 0, -60).Unix() * 1000

	actions := newTestActions(mc)
	toLeave, _, err := actions.FindRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 1)
	assert.Contains(t, toLeave[0].Reason, "inactive")
}

func TestFindRoomsToLeave_ActiveRoomWithKeyword(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Task done recently"},
	}
	// Recent activity
	mc.lastTimestamp["!room1:test"] = time.Now().Unix() * 1000

	actions := newTestActions(mc)
	toLeave, toKeep, err := actions.FindRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 0)
	assert.Len(t, toKeep, 1)
}

func TestFindRoomsToLeave_NoKeyword(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Team standup"},
	}
	mc.lastTimestamp["!room1:test"] = time.Now().AddDate(0, 0, -90).Unix() * 1000

	actions := newTestActions(mc)
	toLeave, toKeep, err := actions.FindRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 0)
	assert.Len(t, toKeep, 1)
}

func TestFindRoomsToLeave_EmptyRoomWithKeyword(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Done channel"},
	}
	mc.lastTimestampErr["!room1:test"] = errors.New("no messages found in room")

	actions := newTestActions(mc)
	toLeave, _, err := actions.FindRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 1)
	assert.Contains(t, toLeave[0].Reason, "no messages")
}

// LeaveRooms tests

func TestLeaveRooms_Success(t *testing.T) {
	mc := newMockClient()
	actions := newTestActions(mc)

	roomsToLeave := []RoomToLeave{
		{Room: helper.Room{ID: "!room1:test", Name: "Room 1"}, Reason: "test"},
		{Room: helper.Room{ID: "!room2:test", Name: "Room 2"}, Reason: "test"},
	}

	count, err := actions.LeaveRooms(context.Background(), roomsToLeave)
	require.NoError(t, err)

	assert.Equal(t, 2, count)
	assert.Len(t, mc.leaveRoomCalls, 2)
}

func TestLeaveRooms_PartialFailure(t *testing.T) {
	mc := newMockClient()
	mc.leaveRoomErr = errors.New("leave failed")
	actions := newTestActions(mc)

	roomsToLeave := []RoomToLeave{
		{Room: helper.Room{ID: "!room1:test", Name: "Room 1"}, Reason: "test"},
	}

	count, err := actions.LeaveRooms(context.Background(), roomsToLeave)
	require.NoError(t, err)

	assert.Equal(t, 0, count)
}

func TestLeaveRooms_Empty(t *testing.T) {
	mc := newMockClient()
	actions := newTestActions(mc)

	count, err := actions.LeaveRooms(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// FindMentionedRooms tests

func TestFindMentionedRooms_Found(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Dev channel"},
	}
	mc.messages["!room1:test"] = []helper.Message{
		{
			ID:        "msg1",
			Sender:    "@other:test",
			Content:   map[string]interface{}{"body": "Hey @testuser: check this"},
			Timestamp: time.Now().Unix() * 1000,
		},
	}

	actions := newTestActions(mc)
	mentioned, err := actions.FindMentionedRooms(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, mentioned, 1)
	assert.Equal(t, "Dev channel", mentioned[0].Name)
}

func TestFindMentionedRooms_NotFound(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Dev channel"},
	}
	mc.messages["!room1:test"] = []helper.Message{
		{
			ID:        "msg1",
			Sender:    "@other:test",
			Content:   map[string]interface{}{"body": "Hello everyone"},
			Timestamp: time.Now().Unix() * 1000,
		},
	}

	actions := newTestActions(mc)
	mentioned, err := actions.FindMentionedRooms(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, mentioned, 0)
}

func TestFindMentionedRooms_GetRoomsError(t *testing.T) {
	mc := newMockClient()
	mc.getRoomsErr = errors.New("connection failed")

	actions := newTestActions(mc)
	_, err := actions.FindMentionedRooms(context.Background(), 30)

	assert.Error(t, err)
}

// MarkAllRoomsAsRead tests

func TestMarkAllRoomsAsRead_Success(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Room 1"},
		{ID: "!room2:test", Name: "Room 2"},
	}

	actions := newTestActions(mc)
	err := actions.MarkAllRoomsAsRead(context.Background())
	require.NoError(t, err)

	assert.Len(t, mc.markAsReadCalls, 2)
}

func TestMarkAllRoomsAsRead_Error(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Room 1"},
	}
	mc.markAsReadErr = errors.New("failed to mark")

	actions := newTestActions(mc)
	err := actions.MarkAllRoomsAsRead(context.Background())

	assert.Error(t, err)
}

// FindInactiveRoomsToLeave tests

func TestFindInactiveRoomsToLeave_InactiveRoom(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Old project"},
	}
	mc.lastTimestamp["!room1:test"] = time.Now().AddDate(0, 0, -60).Unix() * 1000

	actions := newTestActions(mc)
	toLeave, toKeep, err := actions.FindInactiveRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 1)
	assert.Contains(t, toLeave[0].Reason, "inactive")
	assert.Len(t, toKeep, 0)
}

func TestFindInactiveRoomsToLeave_ActiveRoom(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Active project"},
	}
	mc.lastTimestamp["!room1:test"] = time.Now().Unix() * 1000

	actions := newTestActions(mc)
	toLeave, toKeep, err := actions.FindInactiveRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 0)
	assert.Len(t, toKeep, 1)
}

func TestFindInactiveRoomsToLeave_NoMessages(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Empty room"},
	}
	mc.lastTimestampErr["!room1:test"] = errors.New("no messages found in room")

	actions := newTestActions(mc)
	toLeave, _, err := actions.FindInactiveRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	assert.Len(t, toLeave, 1)
	assert.Contains(t, toLeave[0].Reason, "no messages")
}

func TestFindInactiveRoomsToLeave_InactiveWithCloseKeyword(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Ticket closed"},
	}
	mc.lastTimestamp["!room1:test"] = time.Now().AddDate(0, 0, -60).Unix() * 1000

	actions := newTestActions(mc)
	toLeave, _, err := actions.FindInactiveRoomsToLeave(context.Background(), 30)
	require.NoError(t, err)

	// Close keyword rooms should ALSO be left in inactive mode (no keyword filtering)
	assert.Len(t, toLeave, 1)
}

// LeaveByDate tests

func TestLeaveByDate_Success(t *testing.T) {
	mc := newMockClient()
	mc.rooms = []helper.Room{
		{ID: "!room1:test", Name: "Closed ticket"},
	}
	mc.lastTimestamp["!room1:test"] = time.Now().AddDate(0, 0, -60).Unix() * 1000

	actions := newTestActions(mc)
	left, remain, err := actions.LeaveByDate(context.Background(), 30)
	require.NoError(t, err)

	assert.Equal(t, 1, left)
	assert.Equal(t, 0, remain)
}
