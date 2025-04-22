package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/laduwka/matrix-helper/helper"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type ActionService interface {
	FindRoomsToLeave(ctx context.Context, days int) ([]RoomToLeave, []helper.Room, error)
	LeaveRooms(ctx context.Context, roomsToLeave []RoomToLeave) (int, error)
	LeaveByDate(ctx context.Context, days int) (leftCount, remainCount int, err error)

	FindMentionedRooms(ctx context.Context, days int) ([]ActionRoom, error)
	MarkAllRoomsAsRead(ctx context.Context) error
}

type ActionRoom struct {
	ID          string
	Name        string
	WebLink     string
	ElementLink string
}

type ActionsConfig struct {
	MaxConcurrency int
}

func DefaultActionsConfig() ActionsConfig {
	return ActionsConfig{
		MaxConcurrency: 5,
	}
}

type Actions struct {
	client         helper.Client
	log            *logrus.Logger
	maxConcurrency int
}

type ServiceOption func(*Actions)

func WithConcurrency(n int) ServiceOption {
	return func(a *Actions) {
		a.maxConcurrency = n
	}
}

func WithLogger(log *logrus.Logger) ServiceOption {
	return func(a *Actions) {
		a.log = log
	}
}

func NewActions(client helper.Client, log *logrus.Logger, opts ...ServiceOption) ActionService {
	actions := &Actions{
		client:         client,
		log:            log,
		maxConcurrency: DefaultActionsConfig().MaxConcurrency,
	}

	for _, opt := range opts {
		opt(actions)
	}

	return actions
}

type RoomToLeave struct {
	Room   helper.Room
	Reason string
}

func (a *Actions) FindRoomsToLeave(ctx context.Context, days int) ([]RoomToLeave, []helper.Room, error) {
	a.log.Debug("Fetching rooms to analyze")

	rooms, err := a.client.GetRooms(ctx)
	if err != nil {
		return nil, nil, helper.Wrap(err, "failed to fetch rooms")
	}

	var limitTimestamp int64
	if days > 0 {
		limitTimestamp = helper.GetLimitTimestamp(days)
	}

	var roomsToLeave []RoomToLeave
	var roomsToKeep []helper.Room
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, room := range rooms {
		room := room
		g.Go(func() error {

			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			shouldLeave, reason, err := a.evaluateRoom(ctx, room, limitTimestamp, days == 0)
			if err != nil {
				a.log.WithError(err).WithFields(logrus.Fields{
					"room_id":   room.ID,
					"room_name": room.Name,
				}).Debug("Error evaluating room")

				mu.Lock()
				roomsToKeep = append(roomsToKeep, room)
				mu.Unlock()
				return nil
			}

			mu.Lock()
			if shouldLeave {
				roomsToLeave = append(roomsToLeave, RoomToLeave{Room: room, Reason: reason})
			} else {
				roomsToKeep = append(roomsToKeep, room)
			}
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, helper.Wrap(err, "error in room evaluation")
	}

	return roomsToLeave, roomsToKeep, nil
}

func (a *Actions) LeaveRooms(ctx context.Context, roomsToLeave []RoomToLeave) (int, error) {
	if len(roomsToLeave) == 0 {
		return 0, nil
	}

	a.log.Infof("Starting to leave %d rooms", len(roomsToLeave))

	leftCount := 0
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, roomInfo := range roomsToLeave {
		roomInfo := roomInfo
		g.Go(func() error {

			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			room := roomInfo.Room
			if err := a.client.LeaveRoom(ctx, room.ID); err != nil {
				a.log.WithError(err).WithFields(logrus.Fields{
					"room_id":   room.ID,
					"room_name": room.Name,
				}).Warn("Failed to leave room")
				return nil
			}
			time.Sleep(100 * time.Millisecond)
			a.log.WithField("room_name", room.Name).Info("Successfully left room")
			a.log.WithFields(logrus.Fields{
				"room_id":   room.ID,
				"room_name": room.Name,
				"reason":    roomInfo.Reason,
			}).Debug("Room left successfully")

			mu.Lock()
			leftCount++
			mu.Unlock()

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return leftCount, helper.Wrap(err, "error in room leaving process")
	}

	return leftCount, nil
}

func (a *Actions) LeaveByDate(ctx context.Context, days int) (leftCount, remainCount int, err error) {
	a.log.Debug("Starting two-phase leaving process")

	roomsToLeave, roomsToKeep, err := a.FindRoomsToLeave(ctx, days)
	if err != nil {
		return 0, 0, helper.Wrap(err, "failed to identify rooms to leave")
	}

	remainCount = len(roomsToKeep)

	leftCount, err = a.LeaveRooms(ctx, roomsToLeave)
	if err != nil {
		return leftCount, remainCount, helper.Wrap(err, "failed to leave some rooms")
	}

	return leftCount, remainCount, nil
}

func (a *Actions) FindMentionedRooms(ctx context.Context, days int) ([]ActionRoom, error) {
	a.log.Debug("Finding rooms with mentions")

	rooms, err := a.client.GetRooms(ctx)
	if err != nil {
		return nil, helper.Wrap(err, "failed to fetch rooms")
	}

	limitTimestamp := helper.GetLimitTimestamp(days)
	var mentionedRooms []ActionRoom
	var mu sync.Mutex

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, room := range rooms {
		room := room
		g.Go(func() error {

			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			mentioned, err := a.checkRoomMentions(ctx, room.ID, limitTimestamp)
			if err != nil {
				a.log.WithError(err).WithField("room_id", room.ID).Warn("Failed to check mentions")
				return nil
			}

			if mentioned {
				mu.Lock()
				domain := a.client.Config().Domain
				mentionedRooms = append(mentionedRooms, ActionRoom{
					ID:          room.ID,
					Name:        room.Name,
					WebLink:     fmt.Sprintf("https://%s/#/room/%s", domain, room.ID),
					ElementLink: fmt.Sprintf("element://vector/webapp/#/room/%s", room.ID),
				})
				mu.Unlock()
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return mentionedRooms, helper.Wrap(err, "error while checking mentions")
	}

	return mentionedRooms, nil
}

func (a *Actions) MarkAllRoomsAsRead(ctx context.Context) error {
	a.log.Debug("Starting to mark all rooms as read")

	rooms, err := a.client.GetRooms(ctx)
	if err != nil {
		return helper.Wrap(err, "failed to fetch rooms")
	}

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, room := range rooms {
		room := room
		g.Go(func() error {

			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			if err := a.client.MarkRoomAsRead(ctx, room.ID); err != nil {
				a.log.WithError(err).WithField("room_id", room.ID).Error("Failed to mark room as read")
				return helper.Wrap(err, fmt.Sprintf("failed to mark room %s as read", room.ID))
			}
			time.Sleep(100 * time.Millisecond)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return helper.Wrap(err, "failed to mark all rooms as read")
	}

	a.log.Info("Successfully marked all rooms as read")
	return nil
}

func (a *Actions) evaluateRoom(ctx context.Context, room helper.Room, limitTimestamp int64, checkNameOnly bool) (bool, string, error) {
	a.log.WithFields(logrus.Fields{
		"room_id":   room.ID,
		"room_name": room.Name,
	}).Debug("Evaluating room")

	hasCloseKeyword := helper.HasCloseStatusMessage(room.Name)
	if !hasCloseKeyword {
		return false, "", nil
	}

	if checkNameOnly {
		return true, "Room name contains a closing keyword", nil
	}

	lastActivity, err := a.client.GetLastMessageTimestamp(ctx, room.ID)
	if err != nil {

		if strings.Contains(err.Error(), "no messages") ||
			strings.Contains(err.Error(), "empty room") {
			a.log.WithFields(logrus.Fields{
				"room_id":   room.ID,
				"room_name": room.Name,
			}).Debug("Room has no messages, treating as inactive")

			reason := "Room has no messages and name contains closing keyword"
			return true, reason, nil
		}

		a.log.WithError(err).WithFields(logrus.Fields{
			"room_id":   room.ID,
			"room_name": room.Name,
		}).Error("Failed to get last message timestamp")

		return false, "", helper.Wrap(err, "failed to get last message timestamp")
	}

	if lastActivity < limitTimestamp {
		daysInactive := int((time.Now().Unix()*1000 - lastActivity) / (86400 * 1000))
		reason := fmt.Sprintf("Room inactive for %d days and name contains closing keyword", daysInactive)
		return true, reason, nil
	}

	return false, "", nil
}

func (a *Actions) checkRoomMentions(ctx context.Context, roomID string, since int64) (bool, error) {
	messages, err := a.client.GetMessages(ctx, roomID, since)
	if err != nil {
		return false, helper.Wrap(err, "failed to get messages")
	}

	for _, msg := range messages {
		if msg.ContainsMention(a.client.Config().Username) {
			return true, nil
		}
	}

	return false, nil
}
