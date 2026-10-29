package subtitles

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"
)

// Identifying untagged audio (issue #90).
//
// Plenty of releases tag their audio "und", or nothing, and the language rules can't judge
// a track nobody can name: a Must stays unknown, a badge reads "?". Whisper used to listen
// to such a track only when a subtitle needed it (heardAudio). This pass listens to every
// one in the library, once: three short samples put to a vote (detectVote), answered only
// when they agree and are sure. What it hears goes in the probe cache next to the
// subtitle AI's own detections — the same place DetectedAudio reads for Convert's badges
// and the quality profile's language rules — and the media file is never touched. A file
// that changes (size or mtime) is probed afresh, so it's listened to again.
//
// It's background work in every sense: one file at a time, only while no subtitle job is
// queued or running, ffmpeg and whisper at the tool runner's idle priority. It needs the
// local AI (whisper and a model) and the setting below; without either it does nothing.

// keyIdentifyAudio switches the pass. On by default, like the rest of the module: it
// only runs where the local AI is installed, and a library's untagged tracks are each
// listened to once, for a minute and a half of audio.
const (
	keyIdentifyAudio     = "subs_identify_audio"
	defaultIdentifyAudio = true
)

const (
	// identifyIdleWait is how long the pass waits before looking again when the subtitle
	// queue is busy.
	identifyIdleWait = time.Minute
	// identifyMaxFails is how often listening to a file may fail before the pass leaves
	// it alone until the next start (a file ffmpeg can't decode would otherwise be tried
	// on every library pass).
	identifyMaxFails = 2
)

// commentaryRe spots a commentary track by its title. It isn't the film's audio (the
// language rules leave it out), so it isn't worth whisper's time.
var commentaryRe = regexp.MustCompile(`(?i)\bcommentary\b`)

// fileDetector is an aiRunner that can listen to an audio stream of a video directly
// (whisperGen.detectFile).
type fileDetector interface {
	detectFile(ctx context.Context, ffmpeg, video string, stream int, total time.Duration) (DetectedLang, error)
}

// audioIdentify is the pass's state: the files waiting to be looked at, imports first.
type audioIdentify struct {
	mu     sync.Mutex
	queue  []identifyItem
	queued map[string]bool
	failed map[string]int // listening failures by path, this process
	wake   chan struct{}
	// idleWait replaces identifyIdleWait (tests).
	idleWait time.Duration
}

// identifyItem is one file for the pass.
type identifyItem struct{ path, title string }

// IdentifyAudioOn reports whether the background identification is switched on (it also
// needs the local AI to run).
func (s *Service) IdentifyAudioOn(ctx context.Context) bool {
	return s.settings != nil && s.settings.GetBool(ctx, keyIdentifyAudio, defaultIdentifyAudio)
}

// SetIdentifyAudio switches the background identification. Switching it on queues the
// library straight away rather than at the next library pass.
func (s *Service) SetIdentifyAudio(ctx context.Context, on bool) error {
	if err := s.settings.SetBool(ctx, keyIdentifyAudio, on); err != nil {
		return err
	}
	if on {
		go s.identifyLibrary(context.WithoutCancel(ctx))
	}
	return nil
}

// identifier is what can listen, or nil when the pass must not run: switched off, or no
// whisper with a model.
func (s *Service) identifier(ctx context.Context) fileDetector {
	if !s.IdentifyAudioOn(ctx) {
		return nil
	}
	ai := s.aiGen()
	det, ok := ai.(fileDetector)
	if !ok || !ai.available() || !ai.canRun(false) {
		return nil
	}
	return det
}

// tracksToIdentify are a probe's audio tracks the pass listens to: untagged, not a
// commentary, and not listened to before (by the pass or the subtitle AI; an unsure
// answer counts — it isn't asked again).
func tracksToIdentify(mi *mediaInfo) []AudioTrack {
	if mi == nil {
		return nil
	}
	var out []AudioTrack
	for _, t := range mi.Audio {
		if _, heard := mi.Detected[t.Index]; heard || !t.untagged() || commentaryRe.MatchString(t.Title) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// queueIdentify adds files for the pass; front puts them ahead of the rest (an import).
// A file already waiting, or one that has failed too often, isn't added again.
func (s *Service) queueIdentify(front bool, items ...identifyItem) {
	q := &s.identify
	q.mu.Lock()
	if q.queued == nil {
		q.queued = map[string]bool{}
	}
	var add []identifyItem
	for _, it := range items {
		if it.path == "" || q.queued[it.path] || q.failed[it.path] >= identifyMaxFails {
			continue
		}
		q.queued[it.path] = true
		add = append(add, it)
	}
	if front {
		q.queue = append(add, q.queue...)
	} else {
		q.queue = append(q.queue, add...)
	}
	wake := q.wakeLocked()
	q.mu.Unlock()
	if len(add) > 0 {
		nudge(wake)
	}
}

func (q *audioIdentify) wakeLocked() chan struct{} {
	if q.wake == nil {
		q.wake = make(chan struct{}, 1)
	}
	return q.wake
}

// nextIdentify takes the next file, false when none is waiting.
func (s *Service) nextIdentify() (identifyItem, bool) {
	q := &s.identify
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.queue) == 0 {
		return identifyItem{}, false
	}
	it := q.queue[0]
	q.queue = q.queue[1:]
	delete(q.queued, it.path)
	return it, true
}

// dropIdentifyQueue forgets every waiting file: the pass was switched off, or the AI
// went away. The next library pass after it's back queues them again.
func (s *Service) dropIdentifyQueue() {
	q := &s.identify
	q.mu.Lock()
	q.queue, q.queued = nil, nil
	q.mu.Unlock()
}

// identifyLibrary queues every movie and episode file. Called after each library pass;
// files already listened to cost a cache read.
func (s *Service) identifyLibrary(ctx context.Context) {
	if s.identifier(ctx) == nil {
		return
	}
	files := s.repairFiles(ctx)
	items := make([]identifyItem, 0, len(files))
	for _, f := range files {
		items = append(items, identifyItem{f.path, f.job.Title})
	}
	s.queueIdentify(false, items...)
}

// identifyImported queues a movie's file ahead of the library (the import hook).
func (s *Service) identifyImported(ctx context.Context, movieID int64) {
	if s.movies == nil || s.identifier(ctx) == nil {
		return
	}
	if m, err := s.movies.Get(ctx, movieID); err == nil && m.HasFile && m.MovieFilePath != "" {
		s.queueIdentify(true, identifyItem{m.MovieFilePath, m.Title})
	}
}

// identifyEpisodes queues the files of some of a show's episodes ahead of the library.
func (s *Service) identifyEpisodes(ctx context.Context, seriesID int64, want map[epRef]bool) {
	if s.series == nil || len(want) == 0 || s.identifier(ctx) == nil {
		return
	}
	full, err := s.series.Get(ctx, seriesID)
	if err != nil {
		return
	}
	var items []identifyItem
	for _, sn := range full.Seasons {
		for _, e := range sn.Episodes {
			if e.HasFile && e.FilePath != "" && want[epRef{e.SeasonNumber, e.EpisodeNumber}] {
				items = append(items, identifyItem{e.FilePath, fmt.Sprintf("%s - S%02dE%02d", full.Title, e.SeasonNumber, e.EpisodeNumber)})
			}
		}
	}
	s.queueIdentify(true, items...)
}

// identifyWorker is the pass's one worker: it takes the next file once the subtitle
// queue is idle, and sleeps when there's nothing to do.
func (s *Service) identifyWorker(ctx context.Context) {
	s.identify.mu.Lock()
	wake := s.identify.wakeLocked()
	s.identify.mu.Unlock()
	for {
		it, ok := s.nextIdentify()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		if !s.waitQueueIdle(ctx) {
			return
		}
		det := s.identifier(ctx)
		if det == nil {
			s.dropIdentifyQueue()
			continue
		}
		s.identifyFile(ctx, det, it)
	}
}

// waitQueueIdle waits until no subtitle job is queued or running — a subtitle someone is
// waiting for always goes first. false when ctx ends.
func (s *Service) waitQueueIdle(ctx context.Context) bool {
	wait := s.identify.idleWait
	if wait <= 0 {
		wait = identifyIdleWait
	}
	for s.queueBusy() {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
	}
	return ctx.Err() == nil
}

// queueBusy reports a subtitle job queued or running in either lane.
func (s *Service) queueBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingLocked() > 0 || len(s.runs) > 0
}

// identifyFile listens to one file's untagged tracks and records what it heard. A file
// that can't be probed is left for the next pass; one whisper can't listen to counts a
// failure.
func (s *Service) identifyFile(ctx context.Context, det fileDetector, it identifyItem) {
	mi, err := s.probeFile(ctx, it.path)
	if err != nil || mi == nil {
		return
	}
	todo := tracksToIdentify(mi)
	if len(todo) == 0 {
		return
	}
	total := time.Duration(mi.DurationSec * float64(time.Second))
	heard := map[int]DetectedLang{}
	for _, t := range todo {
		d, err := det.detectFile(ctx, s.ffmpeg, it.path, t.Index, total)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.identify.mu.Lock()
			if s.identify.failed == nil {
				s.identify.failed = map[string]int{}
			}
			s.identify.failed[it.path]++
			s.identify.mu.Unlock()
			s.event("warn", fmt.Sprintf("%s: couldn't identify the language of audio track %d: %v", it.title, t.Index+1, shortErr(err, 160)))
			continue
		}
		heard[t.Index] = d
		s.event("info", heardLine(it.title, t.Index, d))
	}
	if len(heard) > 0 {
		s.saveDetected(ctx, it.path, heard)
	}
}

// saveDetected adds what was heard to the file's cached probe as it is now, keeping
// anything a subtitle job heard meanwhile. A file that changed while it was listened to
// is left alone: what was heard may not be in it, and the next pass listens again.
func (s *Service) saveDetected(ctx context.Context, path string, heard map[int]DetectedLang) {
	mi := s.cachedProbe(ctx, path)
	if mi == nil {
		return
	}
	if mi.Detected == nil {
		mi.Detected = map[int]DetectedLang{}
	}
	for i, d := range heard {
		if _, ok := mi.Detected[i]; !ok {
			mi.Detected[i] = d
		}
	}
	s.saveProbe(ctx, path, mi)
}

// heardLine is the activity-log line for one detection.
func heardLine(title string, track int, d DetectedLang) string {
	if d.Lang == "" || d.Prob < detectMinProb {
		return fmt.Sprintf("%s: couldn't tell the language of untagged audio track %d; it stays unknown", title, track+1)
	}
	return fmt.Sprintf("%s: untagged audio track %d sounds like %s (p = %.2f)", title, track+1, d.Lang, d.Prob)
}
