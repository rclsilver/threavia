package controller

import (
	"context"
	"errors"
	"time"

	"github.com/rclsilver/threavia/internal/execution/kube"
)

func (c *Controller) cleanup(ctx context.Context, r *Record) error {
	c.mu.Lock()
	confirmed, name, uid := r.ConfirmedExit, r.PodName, r.PodUID
	c.mu.Unlock()
	if !confirmed {
		return errors.New("execution exit is unconfirmed")
	}
	if uid != "" {
		pod, err := c.api.Pod(ctx, name)
		if err != nil && !kube.IsNotFound(err) {
			return err
		}
		if err == nil {
			if pod.Metadata.UID != uid {
				return errors.New("cleanup Pod identity changed")
			}
			if err = c.api.ReleasePod(ctx, pod); err != nil && !kube.IsNotFound(err) {
				return err
			}
		}
		if err := c.api.DeletePod(ctx, name, uid); err != nil && !kube.IsNotFound(err) {
			return err
		}
	}
	if err := c.api.DeletePolicy(ctx, name); err != nil && !kube.IsNotFound(err) {
		return err
	}
	if err := c.api.DeleteSecret(ctx, kube.WorkerSecretName(name)); err != nil && !kube.IsNotFound(err) {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r.Cleaned = true
	delete(c.rooms, recordKey(r.Profile, r.Request.ID))
	return c.saveLocked()
}
func (c *Controller) collect(ctx context.Context) {
	c.mu.Lock()
	cleanup := []*Record{}
	for key, r := range c.state.data.Records {
		if r.ConfirmedExit && !r.Cleaned {
			cleanup = append(cleanup, r)
		}
		// Requests expire after at most seven days. Retain their tombstones much
		// longer so late delivery cannot replay commands across controller boots.
		if r.ConfirmedExit && r.Cleaned && r.UpdatedAt.Before(time.Now().Add(-30*24*time.Hour)) {
			delete(c.state.data.Records, key)
			delete(c.rooms, key)
		}
	}
	_ = c.saveLocked()
	c.mu.Unlock()
	for _, r := range cleanup {
		_ = c.cleanup(ctx, r)
	}
	// Catch an unadmitted Pod whose create response raced cancellation or was
	// lost after an API timeout. Such a Pod cannot receive a command.
	pods, err := c.api.ListPods(ctx, "execution.threavia.io/controller="+c.cfg.ID)
	if err != nil {
		return
	}
	for _, p := range pods {
		c.mu.Lock()
		var record *Record
		for _, r := range c.state.data.Records {
			if r.PodName == p.Metadata.Name && r.ConfirmedExit && !r.Admitted && (r.PodUID == "" || r.PodUID == p.Metadata.UID) {
				record = r
				r.PodUID = p.Metadata.UID
				r.Cleaned = false
				_ = c.saveLocked()
				break
			}
		}
		c.mu.Unlock()
		if record != nil {
			_ = c.cleanup(ctx, record)
		}
	}
}
