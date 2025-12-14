package dbus

import (
	"syscall"

	"github.com/godbus/dbus/v5"
	"github.com/hashicorp/go-hclog"
	configv1 "github.com/pdf/hyprpanel/proto/hyprpanel/config/v1"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
)

type idleInhibitor struct {
	conn *dbus.Conn
	log  hclog.Logger
	cfg  *configv1.Config_DBUS_IdleInhibitor

	fd      dbus.UnixFD
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

func (i idleInhibitor) IsActive() bool {
	if i.fd == 0 {
		return false
	}
	return true
}

func (i *idleInhibitor) String() string {
	switch eventv1.InhibitTarget(i.fd) {
	case eventv1.InhibitTarget_INHIBIT_TARGET_IDLE:
		return `idle`
	case eventv1.InhibitTarget_INHIBIT_TARGET_SUSPEND:
		return `suspend`
	case eventv1.InhibitTarget_INHIBIT_TARGET_LOGOUT:
		return `logout`
	}
	return `inactive`
}

func (i *idleInhibitor) Inhibit(target eventv1.InhibitTarget) error {
	var what string
	switch target {
	case eventv1.InhibitTarget_INHIBIT_TARGET_LOGOUT:
		what = `shutdown`
	case eventv1.InhibitTarget_INHIBIT_TARGET_SUSPEND:
		what = `sleep`
	case eventv1.InhibitTarget_INHIBIT_TARGET_IDLE:
		what = `idle`
	default:
		i.log.Warn(`Invalid inhibit target`, `target`, target)
		return nil
	}
	obj := i.conn.Object(fdoLogindName, fdoLogindPath)
	if err := obj.Call(
		fdoLogindManagerMethodInhibit,
		0,
		what,
		`hyprpanel`,
		`user request`,
		`block`,
	).Store(&i.fd); err != nil {
		return err
	}
	return nil

}

func (i *idleInhibitor) Uninhibit() error {
	if i.fd == 0 {
		return nil
	} else if err := syscall.Close(int(i.fd)); err != nil {
		return err
	}
	i.fd = 0
	return nil
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
			if i.fd != 0 {
				if err := i.Uninhibit(); err != nil {
					i.log.Error(`Failed uninhibit`, `err`, err)
				}
			}
			return
		}
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
	}

	i.conn.Signal(i.signals)
	if err := i.init(); err != nil {
		return nil, err
	}
	return i, nil
}
