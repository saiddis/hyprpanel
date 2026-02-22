package dbus

import (
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/hashicorp/go-hclog"
	configv1 "github.com/pdf/hyprpanel/proto/hyprpanel/config/v1"
	eventv1 "github.com/pdf/hyprpanel/proto/hyprpanel/event/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	playbackPlaying = `Playing`
	playbackPaused  = `Paused`
)

type mediaPlayer struct {
	cfg *configv1.Config_DBUS_MediaPlayer
	log hclog.Logger

	conn    *dbus.Conn
	players map[string]*player
	signals chan *dbus.Signal

	mu sync.RWMutex

	eventCh chan *eventv1.Event
	readyCh chan struct{}
	quitCh  chan struct{}
}

type player struct {
	metadata      metadata
	identity      string
	desktopEntry  string
	playback      string
	canGoNext     bool
	canGoPrevious bool
	updatedAt     time.Time

	// indicates that the next signal of a player should be skipped
	skipNextSignal bool
}

func (p player) isPlaying() bool {
	return p.playback == playbackPlaying
}

func (p player) eventState() eventv1.MediaPlayerState {
	switch p.playback {
	case playbackPlaying:
		return eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_PLAYING
	case playbackPaused:
		return eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_PAUSED
	}
	return eventv1.MediaPlayerState_MEDIA_PLAYER_STATE_UNSPECIFIED
}

type metadata struct {
	title   string
	artist  string
	album   string
	artUrl  string
	url     string
	trackId dbus.ObjectPath
	length  int64
}

func (m *mediaPlayer) PlayPause() error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodPlayPause)
}

func (m *mediaPlayer) Pause() error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodPause)
}

func (m *mediaPlayer) Next() error {
	_, sender, ok := m.latestPlayerCopy()

	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodNext)
}

func (m *mediaPlayer) Previous() error {
	_, sender, ok := m.latestPlayerCopy()

	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodPrevious)
}

func (m *mediaPlayer) Play() error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodPlay)
}

func (m *mediaPlayer) Stop() error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodStop)
}

func (m *mediaPlayer) Seek(offset int64) error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodSeek, offset)
}

func (m *mediaPlayer) SetPostion(trackId string, pos int64) error {
	_, sender, ok := m.latestPlayerCopy()
	if !ok {
		return nil
	}
	return m.callMediaPlayer(sender, fdoPlayerMethodSetPosition, trackId, pos)
}

func (m *mediaPlayer) watch() {
	select {
	case <-m.quitCh:
		return
	default:
	}
	for {
		select {
		case <-m.readyCh:
			m.readyCh = nil
			continue
		case <-m.quitCh:
			return
		case sig, ok := <-m.signals:
			if !ok {
				m.log.Warn(`signal channel closed`)
				return
			}
			var p player
			switch sig.Name {
			case fdoPropertiesSignalPropertiesChanged:
				p, ok = m.handlePropertiesChanged(sig)
			case fdoSignalNameOwnerChanged:
				p, ok = m.handleNameOwnerChanged(sig)
			default:
				continue
			}
			if ok {
				m.eventCh <- m.mustEvent(p)
			}
		}
	}
}

func (m *mediaPlayer) handleNameOwnerChanged(sig *dbus.Signal) (player, bool) {
	name := sig.Body[0].(string)
	oldOwner := sig.Body[1].(string)
	newOwner := sig.Body[2].(string)

	if !strings.Contains(name, `MediaPlayer2`) {
		return player{}, false
	}

	if newOwner == `` {
		m.mu.Lock()
		delete(m.players, oldOwner)
		m.mu.Unlock()

		p, _, ok := m.latestPlayerCopy()
		return p, ok
	}

	if oldOwner == `` {

		newPlayer := &player{playback: playbackPlaying}

		defer func() {
			m.mu.Lock()
			m.players[newOwner] = newPlayer
			m.mu.Unlock()
		}()

		var props map[string]dbus.Variant

		err := m.conn.Object(name, fdoMediaPlayerPath).
			Call(fdoPropertiesMethodGetAll, 0, fdoMediaPlayerName).
			Store(&props)

		if err != nil {
			m.log.Warn(`failed fetching initial state`, `player`, name, `err`, err)
			return player{}, false
		}

		latestPlayer, _, ok := m.latestPlayerCopy()

		m.mu.Lock()
		defer m.mu.Unlock()

		m.mustApplyPlayerProperties(newPlayer, props)

		// check if new player overlaps with the last one
		if ok && latestPlayer.isPlaying() && newPlayer.isPlaying() {
			m.pauseOthersLocked(newOwner)
		}

		return *newPlayer, true
	}

	return player{}, false
}

func (m *mediaPlayer) handlePropertiesChanged(sig *dbus.Signal) (player, bool) {
	changed := sig.Body[1].(map[string]dbus.Variant)
	if _, ok := changed[`Rate`]; ok {
		p, _, ok := m.latestPlayerCopy()
		return p, ok
	}

	sender := sig.Sender

	m.mu.RLock()
	p, ok := m.players[sender]
	m.mu.RUnlock()
	if !ok {
		p = &player{playback: playbackPlaying}
		defer func() {
			m.mu.Lock()
			m.players[sender] = p
			m.mu.Unlock()
		}()
	} else if p.skipNextSignal {
		p.skipNextSignal = false
		return player{}, false
	}

	latestPlayer, latestSender, ok := m.latestPlayerCopy()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.mustApplyPlayerProperties(p, changed)

	// check if two different players overlap
	if ok && latestSender != sender && latestPlayer.isPlaying() && p.isPlaying() {
		m.pauseOthersLocked(sender)
	}

	return *p, true
}

func (m *mediaPlayer) pauseOthersLocked(current string) {
	for sender, other := range m.players {
		if sender != current && other.isPlaying() {

			// manually set "Paused" playback state and skip the
			// next signal to keep ui in sync
			other.playback = playbackPaused
			other.skipNextSignal = true

			// force the overlapping player to stop
			go m.callMediaPlayer(sender, fdoPlayerMethodPause)
		}
	}
}

func (m *mediaPlayer) callMediaPlayer(sender, method string, args ...interface{}) error {
	return m.conn.Object(sender, fdoMediaPlayerPath).Call(method, 0, args...).Err
}

func (m *mediaPlayer) mustApplyPlayerProperties(p *player, props map[string]dbus.Variant) {

	defer func() {
		p.updatedAt = time.Now()
	}()

	for k, v := range props {
		switch k {
		case fdoPlayerPropertyPlaybackStatus:
			p.playback = v.Value().(string)
		case fdoPlayerPropertyCanGoNext:
			p.canGoNext = v.Value().(bool)
		case fdoPlayerPropertyCanGoPrevious:
			p.canGoPrevious = v.Value().(bool)
		case fdoPlayerPropertyIdentity:
			p.identity = v.Value().(string)
		case fdoPlayerPropertyDesktopEntry:
			p.desktopEntry = v.Value().(string)
		case fdoPlayerPropertyMetadata:
			m.mustParseMetadata(p, v.Value().(map[string]dbus.Variant))
		}
	}
}

func (m *mediaPlayer) mustParseMetadata(p *player, md map[string]dbus.Variant) {
	for k, v := range md {
		switch k {
		case `xesam:title`:
			p.metadata.title = v.Value().(string)
		case `xesam:artist`:
			arr := v.Value().([]string)
			if len(arr) > 0 {
				p.metadata.artist = arr[0]
			}
		case `xesam:album`:
			p.metadata.album = v.Value().(string)
		case `xesam:url`:
			p.metadata.url = v.Value().(string)
		case `mpris:length`:
			p.metadata.length = v.Value().(int64)
		case `mpris:artUrl`:
			p.metadata.artUrl = v.Value().(string)
		case `mpris:trackid`:
			trackId := v.Value().(dbus.ObjectPath)
			p.metadata.trackId = trackId
		}

	}
}

func (m *mediaPlayer) mustEvent(p player) *eventv1.Event {
	event := &eventv1.MediaPlayerValueChange{
		State:         p.eventState(),
		TrackId:       string(p.metadata.trackId),
		CanGoNext:     p.canGoNext,
		CanGoPrevious: p.canGoPrevious,
		UpdatedAt:     timestamppb.New(p.updatedAt),
	}

	if v := p.metadata.title; v != `` {
		event.Title = &v
	}

	if v := p.metadata.artist; v != `` {
		event.Artist = &v
	}

	if v := p.metadata.album; v != `` {
		event.Album = &v
	}

	if v := p.desktopEntry; v != `` {
		event.DesktopEntry = &v
	}

	if v := p.identity; v != `` {
		event.Identity = &v
	}

	if v := p.metadata.length; v != 0 {
		event.LengthUs = &v
	}

	if v := p.metadata.artUrl; v != "" {
		event.ArtUrl = &v
	}

	if v := p.metadata.url; v != "" {
		event.Url = &v
	}

	data, err := anypb.New(event)
	if err != nil {
		m.log.Error(`error creating event`, err)
		return &eventv1.Event{}
	}
	return &eventv1.Event{
		Kind: eventv1.EventKind_EVENT_KIND_MEDIA_PLAYER_CHANGE,
		Data: data,
	}

}
func (m *mediaPlayer) latestPlayerCopy() (player, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var (
		playingPlayer, recentPlayer *player
		playingSender, recentSender string
	)

	for sender, p := range m.players {

		if p.isPlaying() {
			if playingPlayer == nil || p.updatedAt.After(playingPlayer.updatedAt) {
				playingPlayer = p
				playingSender = sender
			}
		}

		if recentPlayer == nil || p.updatedAt.After(recentPlayer.updatedAt) {
			recentPlayer = p
			recentSender = sender
		}
	}

	if playingSender != `` {
		return *playingPlayer, playingSender, true
	}

	if recentSender != `` {
		return *recentPlayer, recentSender, true
	}

	return player{}, ``, false
}

func newMediaPlayer(conn *dbus.Conn, logger hclog.Logger, eventCh chan *eventv1.Event, cfg *configv1.Config_DBUS_MediaPlayer) (*mediaPlayer, error) {
	m := &mediaPlayer{
		conn:    conn,
		cfg:     cfg,
		log:     logger,
		eventCh: eventCh,
		signals: make(chan *dbus.Signal, 10),
		readyCh: make(chan struct{}),
		quitCh:  make(chan struct{}),
		players: make(map[string]*player),
		mu:      sync.RWMutex{},
	}

	err := m.conn.AddMatchSignal(
		dbus.WithMatchInterface(fdoPropertiesName),
		dbus.WithMatchMember(fdoPropertiesMemberPropertiesChanged),
		dbus.WithMatchObjectPath(fdoMediaPlayerPath),
	)
	if err != nil {
		return nil, err
	}

	err = conn.AddMatchSignal(
		dbus.WithMatchInterface(fdoName),
		dbus.WithMatchMember(fdoMemberNameOwnerChanged),
	)
	if err != nil {
		return nil, err
	}

	m.conn.Signal(m.signals)

	return m, m.init()
}

func (m *mediaPlayer) init() error {
	go m.watch()
	close(m.readyCh)
	return nil
}

func (m *mediaPlayer) close() error {
	select {
	case <-m.quitCh:
	default:
	}
	close(m.quitCh)

	m.conn.RemoveSignal(m.signals)

	return m.conn.Close()
}
