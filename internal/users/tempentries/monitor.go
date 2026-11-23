package tempentries

import (
	"context"
	"encoding/binary"
	"errors"
	"math"

	"github.com/canonical/authd/log"
	"golang.org/x/sys/unix"
)

type watchedOwner struct {
	uid  uint32
	name string
}

type ownerMonitor struct {
	epoll   int
	wake    int
	next    int32
	owners  map[int32]watchedOwner
	ready   map[int32]watchedOwner
	done    chan struct{}
	stopped bool
	err     error
}

func (r *PreAuthUserRecords) startMonitor() error {
	epoll, err := unix.EpollCreate1(unix.EPOLL_CLOEXEC)
	if err != nil {
		return err
	}
	wake, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		unix.Close(epoll)
		return err
	}
	if err := unix.EpollCtl(epoll, unix.EPOLL_CTL_ADD, wake,
		&unix.EpollEvent{Events: unix.EPOLLIN}); err != nil {
		unix.Close(wake)
		unix.Close(epoll)
		return err
	}
	r.monitor = &ownerMonitor{
		epoll: epoll, wake: wake,
		owners: make(map[int32]watchedOwner), ready: make(map[int32]watchedOwner),
		done: make(chan struct{}),
	}
	for uid, u := range r.users {
		for i := range u.owners {
			if err := r.monitorOwner(uid, &u.owners[i]); err != nil {
				close(r.monitor.done)
				return err
			}
		}
		r.users[uid] = u
	}
	go r.monitorOwners()
	return nil
}

// The caller holds rwMu when registering owners or waking the monitor.
func (r *PreAuthUserRecords) monitorOwner(uid uint32, owner *reservationOwner) error {
	m := r.monitor
	if err := r.monitorError(); err != nil {
		return err
	}
	if m.next == math.MaxInt32 {
		return errors.New("pre-auth owner monitor exhausted")
	}
	m.next++
	owner.token = m.next
	// Tokens, rather than descriptor numbers, distinguish queued events after FD reuse.
	if err := unix.EpollCtl(m.epoll, unix.EPOLL_CTL_ADD, int(owner.process.File().Fd()),
		&unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLONESHOT, Fd: owner.token}); err != nil {
		return err
	}
	m.owners[owner.token] = watchedOwner{uid: uid, name: owner.name}
	return nil
}

func (r *PreAuthUserRecords) unmonitorOwner(owner reservationOwner) {
	_ = unix.EpollCtl(r.monitor.epoll, unix.EPOLL_CTL_DEL, int(owner.process.File().Fd()), nil)
	delete(r.monitor.owners, owner.token)
	delete(r.monitor.ready, owner.token)
}

func (r *PreAuthUserRecords) monitorError() error {
	if r.monitor == nil {
		return nil
	}
	if r.monitor.stopped {
		return errors.New("pre-auth owner monitor is closed")
	}
	return r.monitor.err
}

func (r *PreAuthUserRecords) wakeMonitor() {
	if r.monitor == nil {
		return
	}
	var data [8]byte
	binary.NativeEndian.PutUint64(data[:], 1)
	// EAGAIN means a wakeup is already pending.
	_, _ = unix.Write(r.monitor.wake, data[:])
}

func (r *PreAuthUserRecords) monitorOwners() {
	m := r.monitor
	defer close(m.done)
	events := make([]unix.EpollEvent, 64)
	timeout := -1
	for {
		n, err := unix.EpollWait(m.epoll, events, timeout)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		r.rwMu.Lock()
		if m.stopped {
			r.rwMu.Unlock()
			return
		}
		if err != nil {
			m.err = err
			r.rwMu.Unlock()
			log.Errorf(context.Background(), "Pre-auth owner monitor failed: %v", err)
			return
		}
		for _, event := range events[:n] {
			if event.Fd == 0 {
				var data [8]byte
				_, _ = unix.Read(m.wake, data[:])
			} else if owner, ok := m.owners[event.Fd]; ok {
				m.ready[event.Fd] = owner
			}
		}
		r.rwMu.Unlock()
		timeout = -1
		if err := r.removeReadyOwners(); err != nil {
			log.Warningf(context.Background(), "Could not release pre-auth reservations: %v", err)
			// Retry failed removals without polling live owners or spinning on dead pidfds.
			timeout = 1000
		}
	}
}

func (r *PreAuthUserRecords) removeReadyOwners() error {
	r.storeMu.Lock()
	defer r.storeMu.Unlock()
	r.rwMu.Lock()
	var names []string
	tokens := make(map[int32]watchedOwner)
	for token, owner := range r.monitor.ready {
		if r.users[owner.uid].pins > 0 {
			continue
		}
		tokens[token] = owner
		names = append(names, owner.name)
	}
	r.rwMu.Unlock()
	if len(names) == 0 {
		return nil
	}
	// Keep IDs reserved, but let NSS lookups proceed while systemd acknowledges removal.
	if err := r.store.Remove(names...); err != nil {
		return err
	}
	r.rwMu.Lock()
	defer r.rwMu.Unlock()
	uids := make(map[uint32]bool)
	for _, owner := range tokens {
		uids[owner.uid] = true
	}
	for uid := range uids {
		u := r.users[uid]
		live := u.owners[:0]
		for _, owner := range u.owners {
			if _, removed := tokens[owner.token]; removed {
				r.unmonitorOwner(owner)
				owner.process.Close()
			} else {
				live = append(live, owner)
			}
		}
		if len(live) == 0 {
			r.deletePreAuthUser(uid)
		} else {
			u.owners = live
			r.users[uid] = u
		}
	}
	return nil
}
