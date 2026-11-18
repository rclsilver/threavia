package postgres_test

import (
	"errors"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

func notificationOf(f fixture, level domain.NotificationLevel, title, message string) *domain.Notification {
	return &domain.Notification{
		Scope: domain.Scope{ProjectID: f.project.ID, SessionID: f.session.ID, RunID: f.run.ID, JobID: f.job.ID},
		Level: level, Title: title, Message: message,
	}
}

func TestCreateNotificationDeduplicatesUnreadOnly(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)

	first := notificationOf(f, domain.NotificationWarning, "Disk", "90% full")
	created, err := store.CreateNotification(ctx, first)
	if err != nil || !created || first.Count != 1 {
		t.Fatalf("first = created %v, count %d, err %v", created, first.Count, err)
	}
	again := notificationOf(f, domain.NotificationWarning, "Disk", "90% full")
	created, err = store.CreateNotification(ctx, again)
	if err != nil || created || again.Count != 2 || again.ID != first.ID {
		t.Fatalf("identical = created %v, count %d, id %s (want %s), err %v", created, again.Count, again.ID, first.ID, err)
	}

	if _, _, err := store.MarkNotificationRead(ctx, f.owner, first.ID); err != nil {
		t.Fatal(err)
	}
	after := notificationOf(f, domain.NotificationWarning, "Disk", "90% full")
	created, err = store.CreateNotification(ctx, after)
	if err != nil || !created || after.ID == first.ID || after.Count != 1 {
		t.Fatalf("after read = created %v, count %d, same id %v, err %v", created, after.Count, after.ID == first.ID, err)
	}
}

func TestListNotificationsOrdersWarningFirst(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	for _, n := range []*domain.Notification{
		notificationOf(f, domain.NotificationWarning, "old warning", "a"),
		notificationOf(f, domain.NotificationInfo, "info", "b"),
		notificationOf(f, domain.NotificationWarning, "new warning", "c"),
	} {
		if _, err := store.CreateNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.ListNotifications(ctx, f.owner, true, "")
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %v, %v", list, err)
	}
	got := []string{list[0].Title, list[1].Title, list[2].Title}
	want := []string{"new warning", "old warning", "info"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	if other, _ := store.ListNotifications(ctx, domain.UserID("someone-else"), true, ""); len(other) != 0 {
		t.Fatalf("another person sees %v", other)
	}
}

func TestMarkNotificationReadIsIdempotentAndOwnerScoped(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	n := notificationOf(f, domain.NotificationInfo, "t", "m")
	if _, err := store.CreateNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.MarkNotificationRead(ctx, domain.UserID("someone-else"), n.ID); !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("another person's read = %v, want ErrNotFound", err)
	}
	read, changed, err := store.MarkNotificationRead(ctx, f.owner, n.ID)
	if err != nil || !changed || read.ReadAt == nil {
		t.Fatalf("first read = %+v, changed %v, err %v", read, changed, err)
	}
	if _, changed, err = store.MarkNotificationRead(ctx, f.owner, n.ID); err != nil || changed {
		t.Fatalf("second read changed %v, err %v", changed, err)
	}
	if unread, _ := store.ListNotifications(ctx, f.owner, true, ""); len(unread) != 0 {
		t.Fatalf("unread after read = %v", unread)
	}
}

func TestMarkNotificationsReadReturnsOnlyWhatChanged(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	for _, title := range []string{"a", "b"} {
		if _, err := store.CreateNotification(ctx, notificationOf(f, domain.NotificationInfo, title, "m")); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := store.MarkNotificationsRead(ctx, f.owner, f.session.ID)
	if err != nil || len(changed) != 2 {
		t.Fatalf("changed = %v, err %v", changed, err)
	}
	if changed, _ = store.MarkNotificationsRead(ctx, f.owner, ""); len(changed) != 0 {
		t.Fatalf("second pass changed %v", changed)
	}
}

func TestNotificationsFollowSessionDeletion(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	if _, err := store.CreateNotification(ctx, notificationOf(f, domain.NotificationInfo, "t", "m")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, f.owner, f.session.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.ListNotifications(ctx, f.owner, false, ""); len(list) != 0 {
		t.Fatalf("notifications outlived their session: %v", list)
	}
}
