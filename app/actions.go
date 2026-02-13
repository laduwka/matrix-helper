package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/laduwka/matrix-helper/helper"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type ProgressFunc func(current, total int, message string)

type ActionService interface {
	FindRoomsToLeave(ctx context.Context, days int) ([]RoomToLeave, []helper.Room, error)
	FindInactiveRoomsToLeave(ctx context.Context, days int) ([]RoomToLeave, []helper.Room, error)
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
	onProgress     ProgressFunc
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

func WithProgress(fn ProgressFunc) ServiceOption {
	return func(a *Actions) {
		a.onProgress = fn
	}
}

func (a *Actions) reportProgress(current, total int, msg string) {
	if a.onProgress != nil {
		a.onProgress(current, total, msg)
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
	a.reportProgress(0, 0, "Fetching room list...")

	rooms, err := a.client.GetRoomsViaSync(ctx)
	if err != nil {
		return nil, nil, helper.Wrap(err, "failed to fetch rooms")
	}

	var limitTimestamp int64
	if days > 0 {
		limitTimestamp = helper.GetLimitTimestamp(days)
	}

	total := len(rooms)
	a.reportProgress(0, total, "Analyzing rooms...")

	var roomsToLeave []RoomToLeave
	var roomsToKeep []helper.Room
	var mu sync.Mutex
	var processed atomic.Int32

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

				cur := int(processed.Add(1))
				a.reportProgress(cur, total, "Analyzing rooms")
				return nil
			}

			mu.Lock()
			if shouldLeave {
				roomsToLeave = append(roomsToLeave, RoomToLeave{Room: room, Reason: reason})
			} else {
				roomsToKeep = append(roomsToKeep, room)
			}
			mu.Unlock()

			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Analyzing rooms")

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

	total := len(roomsToLeave)
	a.log.Infof("Starting to leave %d rooms", total)

	leftCount := 0
	var mu sync.Mutex
	var processed atomic.Int32

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
			if err := a.client.LeaveRoom(ctx, room.ID, room.Name); err != nil {
				a.log.WithError(err).WithFields(logrus.Fields{
					"room_id":   room.ID,
					"room_name": room.Name,
				}).Warn("Failed to leave room")

				cur := int(processed.Add(1))
				a.reportProgress(cur, total, "Leaving rooms")
				return nil
			}

			a.log.WithField("room_name", room.Name).Info("Successfully left room")
			a.log.WithFields(logrus.Fields{
				"room_id":   room.ID,
				"room_name": room.Name,
				"reason":    roomInfo.Reason,
			}).Debug("Room left successfully")

			mu.Lock()
			leftCount++
			mu.Unlock()

			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Leaving rooms")

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
	a.reportProgress(0, 0, "Fetching room list with timeline...")

	rooms, err := a.client.GetRoomsWithTimeline(ctx, 50)
	if err != nil {
		return nil, helper.Wrap(err, "failed to fetch rooms")
	}

	total := len(rooms)
	a.reportProgress(0, total, "Checking mentions...")

	limitTimestamp := helper.GetLimitTimestamp(days)
	username := a.client.Config().Username
	domain := a.client.Config().Domain

	var mentionedRooms []ActionRoom
	var processed atomic.Int32

	// Rooms that need a fallback API call (timeline was limited and no mention found in sync data)
	var fallbackRooms []helper.RoomWithTimeline

	// Phase 1: Check mentions from sync timeline data (no API calls)
	for _, room := range rooms {
		if room.LastActivity > 0 && room.LastActivity < limitTimestamp {
			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Checking mentions")
			continue
		}

		found := false
		for i := range room.TimelineEvents {
			if room.TimelineEvents[i].Timestamp >= limitTimestamp &&
				room.TimelineEvents[i].ContainsMention(username) {
				found = true
				break
			}
		}

		if found {
			mentionedRooms = append(mentionedRooms, ActionRoom{
				ID:          room.ID,
				Name:        room.Name,
				WebLink:     fmt.Sprintf("https://%s/#/room/%s", domain, room.ID),
				ElementLink: fmt.Sprintf("element://vector/webapp/#/room/%s", room.ID),
			})
			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Checking mentions")
			continue
		}

		if room.TimelineLimited {
			fallbackRooms = append(fallbackRooms, room)
		} else {
			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Checking mentions")
		}
	}

	// Phase 2: Fallback API calls only for rooms where timeline was limited
	if len(fallbackRooms) > 0 {
		a.log.Debugf("Falling back to API calls for %d rooms with limited timelines", len(fallbackRooms))

		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		sem := semaphore.NewWeighted(int64(a.maxConcurrency))

		for _, room := range fallbackRooms {
			room := room
			g.Go(func() error {
				if err := sem.Acquire(gctx, 1); err != nil {
					return helper.Wrap(err, "failed to acquire semaphore")
				}
				defer sem.Release(1)

				mentioned, err := a.checkRoomMentions(gctx, room.ID, limitTimestamp)
				if err != nil {
					a.log.WithError(err).WithField("room_id", room.ID).Warn("Failed to check mentions")
					cur := int(processed.Add(1))
					a.reportProgress(cur, total, "Checking mentions")
					return nil
				}

				if mentioned {
					mu.Lock()
					mentionedRooms = append(mentionedRooms, ActionRoom{
						ID:          room.ID,
						Name:        room.Name,
						WebLink:     fmt.Sprintf("https://%s/#/room/%s", domain, room.ID),
						ElementLink: fmt.Sprintf("element://vector/webapp/#/room/%s", room.ID),
					})
					mu.Unlock()
				}

				cur := int(processed.Add(1))
				a.reportProgress(cur, total, "Checking mentions")
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			return mentionedRooms, helper.Wrap(err, "error while checking mentions")
		}
	}

	return mentionedRooms, nil
}

func (a *Actions) MarkAllRoomsAsRead(ctx context.Context) error {
	a.log.Debug("Starting to mark all rooms as read")
	a.reportProgress(0, 0, "Fetching room list...")

	rooms, err := a.client.GetRoomsViaSync(ctx)
	if err != nil {
		return helper.Wrap(err, "failed to fetch rooms")
	}

	total := len(rooms)
	a.reportProgress(0, total, "Marking rooms as read...")

	var processed atomic.Int32

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, room := range rooms {
		room := room
		g.Go(func() error {

			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			if err := a.client.MarkRoomAsRead(ctx, room.ID, room.Name, room.LastEventID); err != nil {
				a.log.WithError(err).WithField("room_id", room.ID).Error("Failed to mark room as read")
				return helper.Wrap(err, fmt.Sprintf("failed to mark room %s as read", room.ID))
			}

			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Marking rooms as read")

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

	var lastActivity int64
	if room.LastActivity > 0 {
		lastActivity = room.LastActivity
	} else {
		var err error
		lastActivity, err = a.client.GetLastMessageTimestamp(ctx, room.ID)
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
	}

	if lastActivity < limitTimestamp {
		daysInactive := int((time.Now().Unix()*1000 - lastActivity) / (86400 * 1000))
		reason := fmt.Sprintf("Room inactive for %d days and name contains closing keyword", daysInactive)
		return true, reason, nil
	}

	return false, "", nil
}

func (a *Actions) FindInactiveRoomsToLeave(ctx context.Context, days int) ([]RoomToLeave, []helper.Room, error) {
	a.log.Debug("Fetching rooms to analyze for inactivity")
	a.reportProgress(0, 0, "Fetching room list...")

	rooms, err := a.client.GetRoomsViaSync(ctx)
	if err != nil {
		return nil, nil, helper.Wrap(err, "failed to fetch rooms")
	}

	total := len(rooms)
	a.reportProgress(0, total, "Analyzing rooms...")

	limitTimestamp := helper.GetLimitTimestamp(days)

	var roomsToLeave []RoomToLeave
	var roomsToKeep []helper.Room
	var mu sync.Mutex
	var processed atomic.Int32

	g, ctx := errgroup.WithContext(ctx)
	sem := semaphore.NewWeighted(int64(a.maxConcurrency))

	for _, room := range rooms {
		room := room
		g.Go(func() error {
			if err := sem.Acquire(ctx, 1); err != nil {
				return helper.Wrap(err, "failed to acquire semaphore")
			}
			defer sem.Release(1)

			shouldLeave, reason, err := a.evaluateRoomForInactiveLeave(ctx, room, limitTimestamp)
			if err != nil {
				a.log.WithError(err).WithFields(logrus.Fields{
					"room_id":   room.ID,
					"room_name": room.Name,
				}).Debug("Error evaluating room for inactivity")

				mu.Lock()
				roomsToKeep = append(roomsToKeep, room)
				mu.Unlock()

				cur := int(processed.Add(1))
				a.reportProgress(cur, total, "Analyzing rooms")
				return nil
			}

			mu.Lock()
			if shouldLeave {
				roomsToLeave = append(roomsToLeave, RoomToLeave{Room: room, Reason: reason})
			} else {
				roomsToKeep = append(roomsToKeep, room)
			}
			mu.Unlock()

			cur := int(processed.Add(1))
			a.reportProgress(cur, total, "Analyzing rooms")

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, helper.Wrap(err, "error in room evaluation")
	}

	return roomsToLeave, roomsToKeep, nil
}

func (a *Actions) evaluateRoomForInactiveLeave(ctx context.Context, room helper.Room, limitTimestamp int64) (bool, string, error) {
	var lastActivity int64
	if room.LastActivity > 0 {
		lastActivity = room.LastActivity
	} else {
		var err error
		lastActivity, err = a.client.GetLastMessageTimestamp(ctx, room.ID)
		if err != nil {
			if strings.Contains(err.Error(), "no messages") ||
				strings.Contains(err.Error(), "empty room") {
				return true, "Room has no messages", nil
			}
			return false, "", helper.Wrap(err, "failed to get last message timestamp")
		}
	}

	if lastActivity < limitTimestamp {
		daysInactive := int((time.Now().Unix()*1000 - lastActivity) / (86400 * 1000))
		reason := fmt.Sprintf("Room inactive for %d days", daysInactive)
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
