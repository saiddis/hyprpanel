package main

import (
	"encoding/xml"
	"errors"

	"github.com/jwijenbergh/puregotk/v4/gdk"
	"github.com/jwijenbergh/puregotk/v4/gio"
	"github.com/jwijenbergh/puregotk/v4/glib"
	"github.com/jwijenbergh/puregotk/v4/gtk"
	configv1 "github.com/pdf/hyprpanel/proto/hyprpanel/config/v1"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
	modulev1 "github.com/pdf/hyprpanel/proto/hyprpanel/module/v1"
	"github.com/pdf/hyprpanel/style"
)

const (
	inhibitTargetIdleLabel       = `Lock`
	inhibitTargetSleepLabel      = `Suspend`
	inhibitTargetShutdownLabel   = `Shutdown`
	idleInhibitorActionNamespace = `idleinhibitor`
)

type idleInhibitor struct {
	*refTracker
	*api
	cfg     *modulev1.IdleInhibitor
	eventCh chan *eventv1.Event
	quitCh  chan struct{}

	actions       map[eventv1.InhibitTarget]*gio.SimpleAction
	active        bool
	icon          *gtk.Image
	iconContainer *gtk.CenterBox
	container     *gtk.Box
	menuRefs      *refTracker
	wrapper       *gtk.Overlay
	actionGroup   *gio.SimpleActionGroup
	menu          *gtk.PopoverMenu
}

func (i *idleInhibitor) build(container *gtk.Box) error {
	i.wrapper = gtk.NewOverlay()
	i.AddRef(i.wrapper.Unref)
	i.wrapper.SetHalign(gtk.AlignCenterValue)
	i.wrapper.SetValign(gtk.AlignCenterValue)
	i.wrapper.SetCanFocus(false)
	i.wrapper.SetFocusOnClick(false)

	i.container = gtk.NewBox(i.orientation, 0)
	i.AddRef(i.container.Unref)
	i.container.SetName(style.IdleInhibitorID)
	i.container.AddCssClass(style.ModuleClass)

	container.Append(&i.wrapper.Widget)

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
	i.iconContainer = gtk.NewCenterBox()
	i.AddRef(i.iconContainer.Unref)
	i.iconContainer.SetCenterWidget(&i.icon.Widget)
	i.container.SetTooltipMarkup(`Idle inhibitor is inactive`)
	i.container.Append(&i.iconContainer.Widget)
	i.wrapper.SetChild(&i.container.Widget)

	if err := i.buildMenu(); err != nil {
		return err
	}

	clickCb := func(ctrl gtk.GestureClick, nPress int, x, y float64) {
		switch ctrl.GetCurrentButton() {
		case uint(gdk.BUTTON_PRIMARY):
			defaultTarget := eventv1.InhibitTarget_INHIBIT_TARGET_IDLE
			if v := i.cfg.DefaultTarget; v > 1 {
				defaultTarget = eventv1.InhibitTarget(v)
			}
			if !i.active {
				if err := i.host.IdleInhibitorInhibit(defaultTarget); err != nil {
					log.Warn(`error uninhibiting target`, defaultTarget, err)
				}
				return
			}
			for t, a := range i.actions {
				if a.GetState().GetBoolean() {
					if err := i.host.IdleInhibitorUninhibit(t); err != nil {
						log.Warn(`error uninhibiting target`, t, err)
					}
				}
			}
		case uint(gdk.BUTTON_SECONDARY):
			if i.menu != nil {
				i.menu.SetPointingTo(nil)
				i.menu.Popup()
			}
		}
	}

	i.AddRef(func() {
		unrefCallback(&clickCb)
	})
	clickController := gtk.NewGestureClick()
	i.AddRef(clickController.Unref)
	clickController.SetButton(0)
	clickController.ConnectReleased(&clickCb)
	i.container.AddController(&clickController.EventController)

	go i.watch()

	return nil
}

func (i *idleInhibitor) events() chan<- *eventv1.Event {
	return i.eventCh
}

func (i *idleInhibitor) close(container *gtk.Box) {
	defer i.Unref()
	container.Remove(&i.container.Widget)
	if i.icon != nil {
		i.icon.Unref()
	}
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
			case evt := <-i.eventCh:
				switch evt.Kind {
				case eventv1.EventKind_EVENT_KIND_IDLE_INHIBITOR_INHIBIT, eventv1.EventKind_EVENT_KIND_IDLE_INHIBITOR_UNINHIBIT:
					data := &eventv1.IdleInhibitorValue{}
					if !evt.Data.MessageIs(data) {
						log.Warn(`Invalid event`, `module`, style.AudioID, `evt`, evt)
						continue
					}
					if err := evt.Data.UnmarshalTo(data); err != nil {
						log.Warn(`Invalid event`, `module`, style.AudioID, `err`, err, `evt`, evt)
						continue
					}
					var cb glib.SourceFunc
					cb = func(uintptr) bool {
						defer unrefCallback(&cb)
						if evt.Kind == eventv1.EventKind_EVENT_KIND_IDLE_INHIBITOR_INHIBIT {
							if err := i.inhibit(data.Target); err != nil {
								log.Warn(`Failed to toggle idle inhibitor`, `err`, err)
							}
						} else {
							if err := i.uninhibit(data.Target); err != nil {
								log.Warn(`Failed to toggle idle inhibitor`, `err`, err)
							}
						}
						return false
					}
					glib.IdleAdd(&cb, 0)
				}
			}
		}
	}

}

func (i *idleInhibitor) inhibit(target eventv1.InhibitTarget) error {
	if i.icon != nil {
		old := i.icon
		defer old.Unref()
		i.icon = nil
	}

	icon, err := createIcon(
		`inhibit-active`,
		int(i.cfg.IconSize),
		i.cfg.IconSymbolic,
		nil,
	)
	if err != nil {
		return err
	}
	i.container.SetTooltipMarkup(`Idle inhibitor is active`)
	i.icon = icon
	i.iconContainer.SetCenterWidget(&i.icon.Widget)
	i.actions[target].SetState(glib.NewVariantBoolean(true))
	i.active = true
	return nil
}

func (i *idleInhibitor) uninhibit(target eventv1.InhibitTarget) error {
	toggleIcon := true
	for t, a := range i.actions {
		active := a.GetState().GetBoolean()
		if t == target && active {
			a.SetState(glib.NewVariantBoolean(false))
		} else if active {
			toggleIcon = false
		}
	}

	if !toggleIcon {
		return nil
	}

	if i.icon != nil {
		old := i.icon
		defer old.Unref()
		i.icon = nil
	}
	icon, err := createIcon(
		`inhibit`,
		int(i.cfg.IconSize),
		i.cfg.IconSymbolic,
		nil,
	)
	if err != nil {
		return err
	}
	i.container.SetTooltipMarkup(`Idle inhibitor is inactive`)
	i.icon = icon
	i.iconContainer.SetCenterWidget(&i.icon.Widget)
	i.actions[target].SetState(glib.NewVariantBoolean(false))
	i.active = false
	return nil
}

func (i *idleInhibitor) buildMenu() error {
	i.actionGroup = gio.NewSimpleActionGroup()
	i.AddRef(i.actionGroup.Unref)
	id, menuXML, err := i.buildMenuXML()
	if err != nil {
		return err
	}

	builder := gtk.NewBuilderFromString(string(menuXML), len(menuXML))
	defer builder.Unref()
	menuObj := builder.GetObject(id)
	if menuObj == nil {
		return errors.New(`menu object not found`)
	}
	defer menuObj.Unref()
	if menuObj != nil {
		menuModel := &gio.MenuModel{}
		menuObj.Cast(menuModel)
		i.menu = gtk.NewPopoverMenuFromModel(menuModel)
		i.AddRef(i.menu.Unref)
		switch i.panelCfg.Edge {
		case configv1.Edge_EDGE_TOP:
			i.menu.SetPosition(gtk.PosBottomValue)
		case configv1.Edge_EDGE_RIGHT:
			i.menu.SetPosition(gtk.PosLeftValue)
		case configv1.Edge_EDGE_BOTTOM:
			i.menu.SetPosition(gtk.PosTopValue)
		case configv1.Edge_EDGE_LEFT:
			i.menu.SetPosition(gtk.PosRightValue)
		}
	}

	i.menu.SetHasArrow(true)
	i.menu.SetAutohide(true)
	i.menu.SetParent(&i.container.Widget)
	i.container.InsertActionGroup(idleInhibitorActionNamespace, i.actionGroup)

	return nil
}

func (i *idleInhibitor) buildMenuXML() (string, []byte, error) {
	x := menuXMLInterface{
		Menu: &menuXMLMenu{
			ID: `idle-inhibitor-menu`,
		},
	}
	section := new(menuXMLMenuSection)
	targets := []struct {
		value eventv1.InhibitTarget
		label string
	}{
		{
			value: eventv1.InhibitTarget_INHIBIT_TARGET_IDLE,
			label: `Lock`,
		},
		{
			value: eventv1.InhibitTarget_INHIBIT_TARGET_SLEEP,
			label: `Suspend`,
		},
		{
			value: eventv1.InhibitTarget_INHIBIT_TARGET_SHUTDOWN,
			label: `Shutdown`,
		},
	}
	for _, t := range targets {
		section.Items = append(section.Items, &menuXMLItem{
			Attributes: []*menuXMLAttribute{
				{
					Name:  `label`,
					Value: t.label,
				},
				{
					Name:  `action`,
					Value: idleInhibitorActionNamespace + `.` + t.label,
				},
			},
		})
		cb := func(a gio.SimpleAction, param uintptr) {
			enabled := a.GetState().GetBoolean()
			if enabled {
				if err := i.host.IdleInhibitorUninhibit(t.value); err != nil {
					log.Warn(`Failed toggling idle inhibitor`, `target`, t, `err`, err)
					return
				}
			} else {
				if err := i.host.IdleInhibitorInhibit(t.value); err != nil {
					log.Warn(`Failed toggling idle inhibitor`, `target`, t, `err`, err)
					return
				}
			}
		}
		act := gio.NewSimpleActionStateful(t.label, nil, glib.NewVariantBoolean(false))
		act.ConnectActivate(&cb)
		i.actionGroup.AddAction(act)
		i.actions[t.value] = act
		i.menuRefs.AddRef(func() {
			unrefCallback(&cb)
		})
	}

	x.Menu.Sections = append(x.Menu.Sections, section)

	if len(x.Menu.Sections) == 0 {
		return ``, nil, errors.New(`empty menu`)
	}

	b, err := xml.Marshal(x)
	return x.Menu.ID, b, err
}

func newIdleInhibitor(cfg *modulev1.IdleInhibitor, a *api) *idleInhibitor {
	i := &idleInhibitor{
		cfg:        cfg,
		refTracker: newRefTracker(),
		api:        a,
		eventCh:    make(chan *eventv1.Event),
		quitCh:     make(chan struct{}),
		menuRefs:   newRefTracker(),
		actions:    make(map[eventv1.InhibitTarget]*gio.SimpleAction),
	}
	i.AddRef(func() {
		close(i.quitCh)
		close(i.eventCh)
		if i.menuRefs != nil {
			i.menuRefs.Unref()
		}
	})
	return i
}
