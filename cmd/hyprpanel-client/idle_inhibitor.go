package main

import (
	"github.com/jwijenbergh/puregotk/v4/gdk"
	"github.com/jwijenbergh/puregotk/v4/gtk"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
	modulev1 "github.com/pdf/hyprpanel/proto/hyprpanel/module/v1"
	"github.com/pdf/hyprpanel/style"
)

type idleInhibitor struct {
	*refTracker
	*api
	cfg     *modulev1.IdleInhibitor
	eventCh chan *eventv1.Event
	quitCh  chan struct{}
	active  bool

	container *gtk.CenterBox
	icon      *gtk.Image
}

func (i *idleInhibitor) build(container *gtk.Box) error {
	i.container = gtk.NewCenterBox()
	i.container.SetName(style.IdleInhibitorID)
	i.container.AddCssClass(style.ModuleClass)

	clickCb := func(ctrl gtk.GestureClick, nPress int, x, y float64) {
		switch ctrl.GetCurrentButton() {
		case uint(gdk.BUTTON_PRIMARY):
			if err := i.host.IdleInhibitorToggle(eventv1.InhibitTarget_INHIBIT_TARGET_IDLE); err != nil {
				log.Warn(`Failed toggling idle inhibitor`, `err`, err)
			} else if err = i.toggle(); err != nil {
				log.Warn(`Failed updating idle inhibitor`, `err`, err)
			}
		}
	}

	i.AddRef(func() {
		unrefCallback(&clickCb)
	})
	clickController := gtk.NewGestureClick()
	clickController.SetButton(1)
	clickController.ConnectReleased(&clickCb)
	i.container.AddController(&clickController.EventController)

	container.Append(&i.container.Widget)

	icon, err := createIcon(
		`inhibit`,
		int(i.cfg.IconSize),
		i.cfg.IconSymbolic,
		nil,
	)
	if err != nil {
		return err
	}
	i.icon = icon
	i.container.SetCenterWidget(&i.icon.Widget)
	i.container.SetTooltipMarkup(`Idle inhibitor is inactive`)

	go i.watch()

	return nil
}

func (i *idleInhibitor) events() chan<- *eventv1.Event {
	return i.eventCh
}

func (i *idleInhibitor) close(container *gtk.Box) {
	defer i.Unref()
	log.Info(`Closing module on request`, `module`, style.IdleInhibitorID)
	container.Remove(&i.container.Widget)
	if i.icon != nil {
		i.icon.Unref()
	}
}

func newIdleInhibitor(cfg *modulev1.IdleInhibitor, a *api) *idleInhibitor {
	i := &idleInhibitor{
		cfg:        cfg,
		refTracker: newRefTracker(),
		api:        a,
		eventCh:    make(chan *eventv1.Event),
		quitCh:     make(chan struct{}),
	}
	i.AddRef(func() {
		close(i.quitCh)
		close(i.eventCh)
	})
	return i
}

func (i *idleInhibitor) watch() {
	for {
		select {
		case <-i.quitCh:
			return
		default:
			select {
			case <-i.quitCh:
				return
			case <-i.eventCh:
			}
		}
	}

}

func (i *idleInhibitor) toggle() error {
	if i.icon != nil {
		old := i.icon
		defer old.Unref()
		i.icon = nil
	}

	var (
		icon *gtk.Image
		err  error
	)

	if i.active {
		icon, err = createIcon(
			`inhibit`,
			int(i.cfg.IconSize),
			i.cfg.IconSymbolic,
			nil,
		)
		i.container.SetTooltipMarkup(`Idle inhibitor is inactive`)
		i.active = false
	} else {
		icon, err = createIcon(
			`inhibit-active`,
			int(i.cfg.IconSize),
			i.cfg.IconSymbolic,
			nil,
		)
		i.container.SetTooltipMarkup(`Idle inhibitor is active`)
		i.active = true
	}

	if err != nil {
		return err
	}

	i.icon = icon
	i.container.SetCenterWidget(&i.icon.Widget)
	return nil
}
