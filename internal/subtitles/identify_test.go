package subtitles

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tristenlammi/arrmada/internal/convert"
	"github.com/tristenlammi/arrmada/internal/quality"
	"github.com/tristenlammi/arrmada/internal/settings"
	"github.com/tristenlammi/arrmada/internal/store"
)

// listeningAI is fakeAI that can listen to a video's audio streams, answering per stream
// and recording which streams it was asked about.
type listeningAI struct {
	*fakeAI
	mu    sync.Mutex
	heard map[int]DetectedLang
	fail  error
	calls []int
}

func (a *listeningAI) detectFile(_ context.Context, _, _ string, stream int, total time.Duration) (DetectedLang, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, stream)
	if total <= 0 {
		return DetectedLang{}, errors.New("no runtime")
	}
	if a.fail != nil {
		return DetectedLang{}, a.fail
	}
	return a.heard[stream], nil
}

func (a *listeningAI) takeCalls() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.calls
	a.calls = nil
	return c
}

// identifyFixture is a Service over a real probe cache and settings, a synthetic video
// file, and an ffprobe that answers probe() (a fresh copy each time, as a real probe is).
func identifyFixture(t *testing.T, probe func() *mediaInfo, ai aiRunner) (*Service, string, *int) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	video := filepath.Join(t.TempDir(), "Film (2021).mkv")
	if err := os.WriteFile(video, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Service{
		settings: settings.NewService(st.DB()), cache: &probeCache{db: st.DB()},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), wake: make(chan struct{}, 1), ai: ai,
	}
	probes := 0
	s.ffprobeRun = func(context.Context, string, string) (*mediaInfo, error) {
		probes++
		mi := probe()
		mi.ProbeVersion = probeVersion
		return mi, nil
	}
	return s, video, &probes
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// Only untagged tracks are listened to: not a tagged one, not a commentary, and not one
// that was listened to before — an unsure answer included.
func TestTracksToIdentify(t *testing.T) {
	mi := &mediaInfo{
		Audio: []AudioTrack{
			{Index: 0, Lang: "eng"}, {Index: 1, Lang: "und"}, {Index: 2}, {Index: 3, Lang: "zxx"},
			{Index: 4, Lang: "mis"}, {Index: 5, Lang: "und", Title: "Director's Commentary"},
			{Index: 6, Lang: "und"}, {Index: 7, Lang: "und"}, {Index: 8, Lang: "ger"}, {Index: 9, Lang: "unknown"},
		},
		Detected: map[int]DetectedLang{6: {"fr", 0.9}, 7: {"de", 0}},
	}
	var got []int
	for _, tr := range tracksToIdentify(mi) {
		got = append(got, tr.Index)
	}
	if want := []int{1, 2, 3, 4, 9}; !slices.Equal(got, want) {
		t.Errorf("tracks %v, want %v", got, want)
	}
	if tracksToIdentify(nil) != nil {
		t.Error("a nil probe has tracks")
	}
}

// A file is listened to once: the confident answers reach DetectedAudio (what Convert's
// badges and the language rules read), an unsure one is kept but never used, a tagged
// track is never asked about, and nothing is asked again until the file changes.
func TestIdentifyFileOnceUntilItChanges(t *testing.T) {
	ai := &listeningAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: map[int]DetectedLang{
		1: {"de", 0.9}, 2: {"en", 0}, 3: {"fr", 0.95},
	}}
	s, video, probes := identifyFixture(t, func() *mediaInfo {
		return &mediaInfo{DurationSec: 5400, AudioLangs: []string{"eng"}, Audio: []AudioTrack{
			{Index: 0, Lang: "eng"}, {Index: 1, Lang: "und"}, {Index: 2}, {Index: 3, Lang: "zxx"},
		}}
	}, ai)
	ctx := t.Context()
	det := s.identifier(ctx)
	if det == nil {
		t.Fatal("no identifier with the AI ready and the setting at its default")
	}
	it := identifyItem{video, "Film"}
	s.identifyFile(ctx, det, it)
	if got := ai.takeCalls(); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("listened to %v, want the untagged tracks 1-3", got)
	}
	want := map[string]map[int]string{video: {1: "de", 3: "fr"}}
	if got := s.DetectedAudio(ctx, map[string]int64{video: fileSize(t, video)}); !reflect.DeepEqual(got, want) {
		t.Errorf("DetectedAudio %v, want %v", got, want)
	}

	s.identifyFile(ctx, det, it)
	if got := ai.takeCalls(); len(got) != 0 || *probes != 1 {
		t.Errorf("second pass listened to %v (probes %d); the answers are cached", got, *probes)
	}

	// The file changes (a new release at the same path): probed and listened to afresh.
	if err := os.WriteFile(video, []byte("a different, longer synthetic video"), 0o644); err != nil {
		t.Fatal(err)
	}
	ai.heard[1] = DetectedLang{"ja", 0.92}
	s.identifyFile(ctx, det, it)
	if got := ai.takeCalls(); !slices.Equal(got, []int{1, 2, 3}) || *probes != 2 {
		t.Errorf("changed file: listened to %v, probes %d", got, *probes)
	}
	if got := s.DetectedAudio(ctx, map[string]int64{video: fileSize(t, video)}); got[video][1] != "ja" {
		t.Errorf("changed file: %v", got)
	}
}

// The pass runs only with the setting on and a local AI that can listen; switched off it
// asks nothing. It's on by default, like the module's automatic modes.
func TestIdentifyGating(t *testing.T) {
	ai := &listeningAI{fakeAI: &fakeAI{avail: true, transcribe: true}}
	s, _, _ := identifyFixture(t, func() *mediaInfo { return &mediaInfo{} }, ai)
	ctx := t.Context()
	if !s.IdentifyAudioOn(ctx) || s.identifier(ctx) == nil {
		t.Fatal("not on by default")
	}
	if err := s.SetIdentifyAudio(ctx, false); err != nil {
		t.Fatal(err)
	}
	if s.identifier(ctx) != nil || s.IdentifyAudioOn(ctx) {
		t.Error("switched off, still identifies")
	}
	if err := s.settings.SetBool(ctx, keyIdentifyAudio, true); err != nil {
		t.Fatal(err)
	}
	ai.avail = false
	if s.identifier(ctx) != nil {
		t.Error("identifies with no whisper")
	}
	ai.avail, ai.transcribe = true, false
	if s.identifier(ctx) != nil {
		t.Error("identifies with no model that can run")
	}
	s.ai = &fakeAI{avail: true, transcribe: true} // an AI that can't listen to a file
	if s.identifier(ctx) != nil {
		t.Error("identifies with an AI that can't detect")
	}
	if (&Service{}).identifier(ctx) != nil {
		t.Error("identifies with no settings")
	}
}

// A failed listen records nothing (the next pass tries again), and a file that keeps
// failing is left alone after identifyMaxFails.
func TestIdentifyFailures(t *testing.T) {
	ai := &listeningAI{fakeAI: &fakeAI{avail: true, transcribe: true}, fail: errors.New("ffmpeg: no decodable audio")}
	s, video, _ := identifyFixture(t, func() *mediaInfo {
		return &mediaInfo{DurationSec: 600, Audio: []AudioTrack{{Index: 0, Lang: "und"}}}
	}, ai)
	ctx := t.Context()
	it := identifyItem{video, "Film"}
	for i := 0; i < identifyMaxFails; i++ {
		s.queueIdentify(false, it)
		got, ok := s.nextIdentify()
		if !ok || got != it {
			t.Fatalf("try %d: not queued (%v %v)", i+1, got, ok)
		}
		s.identifyFile(ctx, s.identifier(ctx), got)
	}
	if mi := s.cachedProbe(ctx, video); mi == nil || len(mi.Detected) != 0 {
		t.Errorf("a failure was recorded: %+v", mi)
	}
	s.queueIdentify(true, it)
	if _, ok := s.nextIdentify(); ok {
		t.Error("a file that keeps failing was queued again")
	}
}

// Imports go to the front, the library to the back, and a file waiting already isn't
// queued twice.
func TestIdentifyQueueOrder(t *testing.T) {
	s := &Service{}
	s.queueIdentify(false, identifyItem{"/a", "A"}, identifyItem{"/b", "B"}, identifyItem{"", "none"})
	s.queueIdentify(true, identifyItem{"/new", "New"}, identifyItem{"/b", "B"})
	var got []string
	for {
		it, ok := s.nextIdentify()
		if !ok {
			break
		}
		got = append(got, it.path)
	}
	if want := []string{"/new", "/a", "/b"}; !slices.Equal(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}

// The worker waits while a subtitle job runs, then listens; switched off, it drops what
// was waiting.
func TestIdentifyWorkerWaitsForTheQueue(t *testing.T) {
	ai := &listeningAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: map[int]DetectedLang{0: {"en", 0.97}}}
	s, video, _ := identifyFixture(t, func() *mediaInfo {
		return &mediaInfo{DurationSec: 1300, Audio: []AudioTrack{{Index: 0, Lang: "und"}}}
	}, ai)
	s.identify.idleWait = 5 * time.Millisecond
	s.runs = map[int64]*runHandle{1: {}} // a subtitle job is running
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.identifyWorker(ctx) }()
	defer func() { cancel(); <-done }()

	s.queueIdentify(false, identifyItem{video, "Film"})
	time.Sleep(50 * time.Millisecond)
	if got := ai.takeCalls(); len(got) != 0 {
		t.Fatalf("listened while a subtitle job ran: %v", got)
	}
	s.mu.Lock()
	delete(s.runs, 1)
	s.mu.Unlock()
	size := fileSize(t, video)
	deadline := time.Now().Add(5 * time.Second)
	for len(s.DetectedAudio(ctx, map[string]int64{video: size})) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the worker never listened once the queue was idle")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Off: whatever is waiting is dropped, not listened to.
	if err := s.settings.SetBool(ctx, keyIdentifyAudio, false); err != nil {
		t.Fatal(err)
	}
	ai.takeCalls()
	other := filepath.Join(filepath.Dir(video), "Other (2022).mkv")
	if err := os.WriteFile(other, []byte("synthetic"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.queueIdentify(false, identifyItem{other, "Other"})
	deadline = time.Now().Add(5 * time.Second)
	for {
		s.identify.mu.Lock()
		n := len(s.identify.queue)
		s.identify.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the queue wasn't taken")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if got := ai.takeCalls(); len(got) != 0 {
		t.Errorf("listened while switched off: %v", got)
	}
}

// What the pass hears reaches the language rules the way Convert reads it: an untagged
// file nobody listened to is unknown to a Must (no verdict, no upgrade); once heard as
// English it's known to lack a German Must, and meets an English one.
func TestIdentifiedLanguageReachesTheRules(t *testing.T) {
	ai := &listeningAI{fakeAI: &fakeAI{avail: true, transcribe: true}, heard: map[int]DetectedLang{0: {"en", 0.95}}}
	s, video, _ := identifyFixture(t, func() *mediaInfo {
		return &mediaInfo{DurationSec: 6000, Audio: []AudioTrack{{Index: 0, Lang: "und"}}}
	}, ai)
	ctx := t.Context()
	size := fileSize(t, video)
	analysed := &convert.MediaInfo{VideoCodec: "h264", Resolution: "1080p", Audio: []convert.AudioStream{{AudIndex: 0, Lang: "und"}}}
	fit := func(rule string, heard map[int]string) string {
		facts := convert.FactsHeard(analysed, heard)
		sp := quality.StoredProfile{MediaType: quality.MediaMovie, Ideal: &quality.IdealFile{Language: map[string]string{rule: quality.PrefMust}}}
		f := sp.LanguageFit(quality.CurrentFile{Release: "Film.2021.1080p.WEB-DL.x264-GRP", Facts: &facts, OrigLanguage: "en"})
		if f == nil {
			return "unknown"
		}
		return f.Status
	}
	before := s.DetectedAudio(ctx, map[string]int64{video: size})[video]
	if got := fit("de", before); got != "unknown" {
		t.Errorf("unheard, German Must: %s, want unknown", got)
	}
	s.identifyFile(ctx, s.identifier(ctx), identifyItem{video, "Film"})
	heard := s.DetectedAudio(ctx, map[string]int64{video: size})[video]
	if got := fit("de", heard); got != "bad" {
		t.Errorf("heard English, German Must: %s, want bad", got)
	}
	if got := fit("en", heard); got != "good" {
		t.Errorf("heard English, English Must: %s, want good", got)
	}
	if b := convert.AudioBadges(analysed, heard); len(b) != 1 || b[0].Code != "en" || !b[0].Detected {
		t.Errorf("badge %+v, want a detected EN", b)
	}
}
