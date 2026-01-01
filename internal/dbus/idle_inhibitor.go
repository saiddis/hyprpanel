package dbus

import (
	"fmt"
	"strings"
	"sync"
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/hashicorp/go-hclog"
	configv1 "github.com/pdf/hyprpanel/proto/hyprpanel/config/v1"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

type idleInhibitor struct {
	conn *dbus.Conn
	log  hclog.Logger
	cfg  *configv1.Config_DBUS_IdleInhibitor

	mu      sync.RWMutex
	targets map[eventv1.InhibitTarget]dbus.UnixFD
	eventCh chan *eventv1.Event
	signals chan *dbus.Signal
	readyCh chan struct{}
	quitCh  chan struct{}
}

func (i *idleInhibitor) init() error {
	go i.watch()
	i.readyCh <- struct{}{}
	return nil
}

func (i *idleInhibitor) Inhibit(target eventv1.InhibitTarget) error {
	var what string
	switch target {
	case eventv1.InhibitTarget_INHIBIT_TARGET_SHUTDOWN:
		what = fdoIdleInhibitorPropertyShutdown
	case eventv1.InhibitTarget_INHIBIT_TARGET_SLEEP:
		what = fdoIdleInhibitorPropertySleep
	case eventv1.InhibitTarget_INHIBIT_TARGET_IDLE:
		what = fdoIdleInhibitorPropertyIdle
	default:
		return fmt.Errorf(`invalid inhibit target: %v`, target)
	}
	var fd dbus.UnixFD
	obj := i.conn.Object(fdoLogindName, fdoLogindPath)
	if err := obj.Call(
		fdoLogindManagerMethodInhibit,
		0,
		what,
		`hyprpanel`,
		`user request`,
		`block`,
	).Store(&fd); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.targets[target] = fd
	return nil
}

func (i *idleInhibitor) Uninhibit(t eventv1.InhibitTarget) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.targets[t] == 0 {
		return nil
	} else if fd, ok := i.targets[t]; ok {
		if err := syscall.Close(int(fd)); err != nil {
			return err
		}
		i.targets[t] = 0
		return nil
	}
	return fmt.Errorf(`could not find file descriptior for target: %d`, t)
}

func (i *idleInhibitor) watch() {
	select {
	case <-i.quitCh:
		return
	default:
	}
	for {
		select {
		case <-i.readyCh:
			close(i.readyCh)
			i.readyCh = nil
			continue
		case <-i.quitCh:
			return
		case sig, ok := <-i.signals:
			if !ok {
				return
			}
			switch sig.Name {
			case fdoPropertiesSignalPropertiesChanged:
				kind, ok := sig.Body[0].(string)
				if !ok {
					i.log.Warn(`Failed asserting DBUS PropertiesChanged body kind`, `kind`, sig.Body[0])
					continue
				}
				if kind != fdoLogindManagerName {
					continue
				}

				properties, ok := sig.Body[1].(map[string]dbus.Variant)
				if !ok {
					i.log.Warn(`Failed asserting DBUS PropertiesChanged body properties`, `properties`, sig.Body[1])
					continue
				}
				if len(properties) == 0 {
					continue
				}
				pathVar, ok := properties[`BlockInhibited`]
				if !ok {
					continue
				}
				var targets string
				if err := pathVar.Store(&targets); err != nil {
					i.log.Warn(`Failed parsing SysFSPath`, `pathVar`, pathVar, `err`, err)
					continue
				}

				if err := i.updateTargets(targets); err != nil {
					i.log.Warn(`error updating targets: %v`, err)
				}
			}
		}
	}
}

func (i *idleInhibitor) updateTargets(raw string) error {
	active := map[eventv1.InhibitTarget]bool{}
	for _, part := range strings.Split(raw, ":") {
		switch part {
		case fdoIdleInhibitorPropertyShutdown:
			active[eventv1.InhibitTarget_INHIBIT_TARGET_SHUTDOWN] = true
		case fdoIdleInhibitorPropertySleep:
			active[eventv1.InhibitTarget_INHIBIT_TARGET_SLEEP] = true
		case fdoIdleInhibitorPropertyIdle:
			active[eventv1.InhibitTarget_INHIBIT_TARGET_IDLE] = true
		}
	}

	i.mu.RLock()
	defer i.mu.RUnlock()
	for t := range i.targets {
		if active[t] {
			i.eventCh <- i.mustEvent(t, true)
		} else {
			i.eventCh <- i.mustEvent(t, false)
		}
	}

	return nil
}

func (i *idleInhibitor) mustEvent(t eventv1.InhibitTarget, enable bool) *eventv1.Event {
	v := &eventv1.IdleInhibitorValue{
		Target: t,
	}
	data, err := anypb.New(v)
	if err != nil {
		i.log.Error(`error creating event`, err)
		return nil
	}
	kind := eventv1.EventKind_EVENT_KIND_IDLE_INHIBITOR_UNINHIBIT
	if enable {
		kind = eventv1.EventKind_EVENT_KIND_IDLE_INHIBITOR_INHIBIT
	}
	return &eventv1.Event{
		Kind: kind,
		Data: data,
	}
}

func newIdleInhibitor(conn *dbus.Conn, logger hclog.Logger, eventCh chan *eventv1.Event, cfg *configv1.Config_DBUS_IdleInhibitor) (*idleInhibitor, error) {
	i := &idleInhibitor{
		conn:    conn,
		log:     logger,
		cfg:     cfg,
		eventCh: eventCh,
		signals: make(chan *dbus.Signal),
		readyCh: make(chan struct{}),
		quitCh:  make(chan struct{}),
		mu:      sync.RWMutex{},
		targets: map[eventv1.InhibitTarget]dbus.UnixFD{
			eventv1.InhibitTarget_INHIBIT_TARGET_IDLE:     0,
			eventv1.InhibitTarget_INHIBIT_TARGET_SLEEP:    0,
			eventv1.InhibitTarget_INHIBIT_TARGET_SHUTDOWN: 0,
		},
	}

	if err := i.conn.AddMatchSignal(
		dbus.WithMatchInterface(fdoPropertiesName),
		dbus.WithMatchObjectPath(fdoLogindPath),
	); err != nil {
		return nil, err
	}

	i.conn.Signal(i.signals)
	if err := i.init(); err != nil {
		return nil, err
	}
	return i, nil
}

func (i *idleInhibitor) close() error {
	select {
	case <-i.quitCh:
	default:
	}
	close(i.quitCh)

	i.mu.Lock()
	defer i.mu.Unlock()

	for t, fd := range i.targets {
		if fd != 0 {
			_ = syscall.Close(int(fd))
			i.targets[t] = 0
		}
	}

	return nil
}
