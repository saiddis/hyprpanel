package main

import (
	"github.com/jwijenbergh/puregotk/v4/gdk"
	"github.com/jwijenbergh/puregotk/v4/gio"
	"github.com/jwijenbergh/puregotk/v4/gtk"
	configv1 "github.com/pdf/hyprpanel/proto/hyprpanel/config/v1"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
	modulev1 "github.com/pdf/hyprpanel/proto/hyprpanel/module/v1"
	"github.com/pdf/hyprpanel/style"
)

const mediaPlayerActionNamespace = `mediaplayer`

type mediaPlayer struct {
	*refTracker
	*api
	cfg     *modulev1.MediaPlayer
	eventCh chan *eventv1.Event
	quitCh  chan struct{}

	titleLabel    *gtk.Label
	artistLabel   *gtk.Label
	playButton    *gtk.Button
	prevButton    *gtk.Button
	nextButton    *gtk.Button
	icon          *gtk.Image
	iconContainer *gtk.CenterBox
	container     *gtk.Box
	menuRefs      *refTracker
	actionGroup   *gio.SimpleActionGroup
	popover       *gtk.Popover
}

func (m *mediaPlayer) build(container *gtk.Box) error {
	m.container = gtk.NewBox(m.orientation, 0)
	m.AddRef(m.container.Unref)
	m.container.SetName(style.MediaPlayerID)
	m.container.AddCssClass(style.ModuleClass)

	icon, err := createIcon(
		`media-audio`,
		int(m.cfg.IconSize),
		m.cfg.IconSymbolic,
		nil,
	)
	if err != nil {
		return err
	}
	m.icon = icon
	m.iconContainer = gtk.NewCenterBox()
	m.AddRef(m.iconContainer.Unref)
	m.iconContainer.SetCenterWidget(&icon.Widget)

	m.container.Append(&m.iconContainer.Widget)

	m.initActions()

	if err := m.buildMenu(); err != nil {
		return err
	}

	m.container.InsertActionGroup(mediaPlayerActionNamespace, m.actionGroup)

	clickCb := func(ctrl gtk.GestureClick, nPress int, x, y float64) {
		switch ctrl.GetCurrentButton() {
		case uint(gdk.BUTTON_PRIMARY):
			if err := m.host.MediaPlayerPlayPause(); err != nil {
				log.Warn(`Media player play/pause failed`, `err`, err)
			}
		case uint(gdk.BUTTON_SECONDARY):
			if m.popover != nil {
				m.popover.SetPointingTo(nil)
				m.popover.Popup()
			}
		case uint(gdk.BUTTON_MIDDLE):
			if err := m.host.MediaPlayerStop(); err != nil {
				log.Warn(`Media player stop failed`, `err`, err)
			}
		}
	}

	m.AddRef(func() {
		unrefCallback(&clickCb)
	})
	clickController := gtk.NewGestureClick()
	m.AddRef(clickController.Unref)
	clickController.SetButton(0)
	clickController.ConnectReleased(&clickCb)
	m.container.AddController(&clickController.EventController)

	scrollCb := func(_ gtk.EventControllerScroll, dx, dy float64) bool {
		if err := m.host.MediaPlayerSeek(int64(dy - dy*2)); err != nil {
			log.Warn(`Media player seek failed`, `err`, err)
		}
		return true
	}

	m.AddRef(func() {
		unrefCallback(&scrollCb)
	})

	scrollController := gtk.NewEventControllerScroll(gtk.EventControllerScrollDiscreteValue | gtk.EventControllerScrollVerticalValue)
	scrollController.ConnectScroll(&scrollCb)
	m.container.AddController(&scrollController.EventController)
	m.AddRef(scrollController.Unref)

	container.Append(&m.container.Widget)

	go m.watch()

	return nil
}

func (m *mediaPlayer) watch() {
	for {
		select {
		case <-m.quitCh:
			return
		default:
		}
		select {
		case <-m.quitCh:
			return
		case evt := <-m.eventCh:
			switch evt.Kind {
			case eventv1.EventKind_EVENT_KIND_MEDIA_PLAYER_CHANGE:
				log.Info(`Media player change`, evt.Kind)
				data := &eventv1.MediaPlayerValueChange{}
				if !evt.Data.MessageIs(data) {
					log.Warn(`Invalid event`, `module`, style.AudioID, `evt`, evt)
					continue
				}
				if err := evt.Data.UnmarshalTo(data); err != nil {
					log.Warn(`Invalid event`, `module`, style.AudioID, `err`, err, `evt`, evt)
					continue
				}
				m.update(data)
			}
		}
	}
}

func (m *mediaPlayer) update(e *eventv1.MediaPlayerValueChange) {
	log.Info(`Media player state`, e.State)
	switch e.State {
	case eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_UNSPECIFIED:
		m.setDefaultState(e)

	case eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING:
		m.setPlayingState(e)

	case eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED:
		m.setPausedState(e)

	case eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_STOPPED:
		m.setDefaultState(e)
	}
}

func (m *mediaPlayer) setDefaultState(e *eventv1.MediaPlayerValueChange) {
	m.container.SetTooltipMarkup(`Nothing playing`)

	m.titleLabel.SetLabel(`Nothing playing`)
	m.artistLabel.SetLabel(``)

	m.prevButton.SetSensitive(false)
	m.playButton.SetSensitive(false)
	m.nextButton.SetSensitive(false)

	m.playButton.SetIconName(`media-playback-start-symbolic`)

	m.icon.SetFromIconName(`media-audio-symbolic`)
}

func (m *mediaPlayer) setPlayingState(e *eventv1.MediaPlayerValueChange) {
	title := valueOr(e.Title, `Unknown title`)
	artist := valueOr(e.Artist, ``)

	m.titleLabel.SetLabel(title)
	m.artistLabel.SetLabel(artist)

	m.container.SetTooltipMarkup(title)

	m.prevButton.SetSensitive(e.CanGoPrevious)
	m.nextButton.SetSensitive(e.CanGoNext)
	m.playButton.SetSensitive(true)

	m.playButton.SetIconName(`media-playback-pause-symbolic`)
	m.icon.SetFromIconName(`mediaplayer-app-symbolic`)
}

func (m *mediaPlayer) setPausedState(e *eventv1.MediaPlayerValueChange) {
	title := valueOr(e.Title, `Unknown title`)
	artist := valueOr(e.Artist, ``)

	m.titleLabel.SetLabel(title)
	m.artistLabel.SetLabel(artist)

	m.container.SetTooltipMarkup(`Paused: ` + title)

	m.prevButton.SetSensitive(e.CanGoPrevious)
	m.nextButton.SetSensitive(e.CanGoNext)
	m.playButton.SetSensitive(true)

	m.playButton.SetIconName(`media-playback-start-symbolic`)

	m.icon.SetFromIconName(`media-playback-pause-symbolic`)
}

func (m *mediaPlayer) buildMenu() error {
	popover := gtk.NewPopover()
	m.AddRef(popover.Unref)

	popover.SetAutohide(true)
	popover.SetHasArrow(true)
	popover.SetParent(&m.container.Widget)

	switch m.panelCfg.Edge {
	case configv1.Edge_EDGE_TOP:
		popover.SetPosition(gtk.PosBottomValue)
	case configv1.Edge_EDGE_RIGHT:
		popover.SetPosition(gtk.PosLeftValue)
	case configv1.Edge_EDGE_BOTTOM:
		popover.SetPosition(gtk.PosTopValue)
	case configv1.Edge_EDGE_LEFT:
		popover.SetPosition(gtk.PosRightValue)
	}

	root := gtk.NewBox(gtk.OrientationVerticalValue, 12)
	root.SetMarginTop(16)
	root.SetMarginBottom(16)
	root.SetMarginStart(16)
	root.SetMarginEnd(16)

	title := gtk.NewLabel(`Nothing playing`)
	title.AddCssClass(style.MediaPlayerTitleClass)

	artist := gtk.NewLabel(``)
	artist.AddCssClass(style.MediaPlayerArtistClass)

	controls := gtk.NewBox(gtk.OrientationHorizontalValue, 12)
	controls.SetHalign(gtk.AlignCenterValue)

	prev := gtk.NewButtonFromIconName(`media-skip-backward-symbolic`)
	play := gtk.NewButtonFromIconName(`media-playback-start-symbolic`)
	next := gtk.NewButtonFromIconName(`media-skip-forward-symbolic`)

	play.SetActionName(mediaPlayerActionNamespace + `.play-pause`)
	play.AddCssClass(style.MediaPlayerButtonClass)
	play.SetSensitive(false)

	prev.SetActionName(mediaPlayerActionNamespace + `.previous`)
	prev.SetSensitive(false)

	next.SetActionName(mediaPlayerActionNamespace + `.next`)
	next.SetSensitive(false)

	controls.Append(&prev.Widget)
	controls.Append(&play.Widget)
	controls.Append(&next.Widget)

	root.Append(&title.Widget)
	root.Append(&artist.Widget)
	root.Append(&controls.Widget)

	popover.SetChild(&root.Widget)
	m.container.Append(&popover.Widget)
	m.titleLabel = title
	m.artistLabel = artist
	m.playButton = play
	m.prevButton = prev
	m.nextButton = next
	m.popover = popover

	return nil
}

func (m *mediaPlayer) initActions() {
	m.actionGroup = gio.NewSimpleActionGroup()
	m.AddRef(m.actionGroup.Unref)

	actions := []string{
		`previous`,
		`play-pause`,
		`next`,
	}

	for _, name := range actions {
		act := gio.NewSimpleAction(name, nil)

		cb := func(a gio.SimpleAction, _ uintptr) {
			switch a.GetName() {
			case `previous`:
				if err := m.host.MediaPlayerPrevious(); err != nil {
					log.Error(`Media player previous failed`, `err`, err)
				}
			case `play-pause`:
				if err := m.host.MediaPlayerPlayPause(); err != nil {
					log.Error(`Media player play/pause failed`, `err`, err)
				}
			case `next`:
				if err := m.host.MediaPlayerNext(); err != nil {
					log.Error(`Media player next failed`, `err`, err)
				}
			}
		}

		act.ConnectActivate(&cb)
		m.actionGroup.AddAction(act)
	}
}

func (m *mediaPlayer) events() chan<- *eventv1.Event {
	return m.eventCh
}

func newMediaPlayer(cfg *modulev1.MediaPlayer, a *api) *mediaPlayer {
	m := &mediaPlayer{
		cfg:        cfg,
		refTracker: newRefTracker(),
		api:        a,
		eventCh:    make(chan *eventv1.Event),
		quitCh:     make(chan struct{}),
		menuRefs:   newRefTracker(),
	}
	m.AddRef(func() {
		close(m.quitCh)
		close(m.eventCh)
		if m.menuRefs != nil {
			m.menuRefs.Unref()
		}
	})
	return m
}

func (m *mediaPlayer) close(container *gtk.Box) {
	defer m.Unref()
	container.Remove(&m.container.Widget)
	if m.icon != nil {
		m.icon.Unref()
	}
}

func valueOr(v *string, fallback string) string {
	if v == nil || *v == `` {
		return fallback
	}
	return *v
}
