package quality

import (
	"strconv"
	"testing"

	"github.com/tristenlammi/arrmada/internal/parser"
)

// The planner leaves a starved pack that overlaps better grabs for a later sweep, once the
// better files are on disk (automation's planner.mustWait). That only works if the import
// then refuses the starved pack's copies of those episodes, with upgrades on or off: no
// bitrate gain (far smaller), and no quality gain. (IsQualityUpgrade scores the name
// without a runtime, so it's the missing gain that refuses it there, not the floor.)
func TestStarvedPackEpisodeIsNeverAnUpgrade(t *testing.T) {
	s, ctx := testService(t)
	for _, upgrades := range []bool{true, false} {
		p, err := s.Create(ctx, StoredProfile{
			MediaType: MediaSeries, Name: "floor " + strconv.FormatBool(upgrades),
			UpgradesEnabled: upgrades, UpgradeMinPercent: 10,
			Ideal: &IdealFile{Bitrate: map[string]BitrateWindow{"1080p": {Min: 5}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		ref := "custom:" + strconv.FormatInt(p.ID, 10)
		const (
			clean   = "Luther.S01E01.1080p.BluRay.x264-GRP"                        // 4 GB over 60 min: ~9 Mb/s
			starved = "Luther (2010) S01-S05 COMPLETE 1080p H265 EAC3 2CH-MNKYDDL" // 0.72 GB an episode: ~1.6 Mb/s
		)
		cur := cf(clean, 4, 60)
		if s.IsQualityUpgrade(ctx, ref, starved, 0.72, cur) {
			t.Errorf("upgrades=%v: the starved pack's episode passed as a quality upgrade of the clean file", upgrades)
		}
		if s.IsBitrateUpgrade(ctx, ref, Encode{SizeGB: 0.72, Codec: parser.CodecX265}, Encode{SizeGB: 4, Codec: parser.CodecX264}, 60) {
			t.Errorf("upgrades=%v: the starved pack's episode passed as a bitrate upgrade of the clean file", upgrades)
		}
	}
}

// GrabTier follows Decide's two leading keys: the avoided tier (by the ranking's own rule),
// then health within it.
func TestGrabTier(t *testing.T) {
	live := NewCandidate("Show.S01.1080p.WEB-DL.x264-GRP", 8, 10)
	dead := NewCandidate("Show.S01.1080p.WEB-DL.x264-GRP", 8, 0)
	usenet := dead
	usenet.Usenet = true
	cases := []struct {
		name    string
		ev      Evaluation
		ranking string
		want    int
	}{
		{"clean and live", Evaluation{Candidate: live}, RankingV1, 0},
		{"clean, dead torrent", Evaluation{Candidate: dead}, RankingV1, 1},
		{"usenet is never dead", Evaluation{Candidate: usenet}, RankingV1, 0},
		{"avoided, live", Evaluation{Candidate: live, Avoided: true, TargetAvoided: true}, RankingV2, 2},
		{"avoided and dead", Evaluation{Candidate: dead, Avoided: true, TargetAvoided: true}, RankingV1, 3},
		{"classic: a custom avoid is the tier", Evaluation{Candidate: live, Avoided: true, CustomAvoided: true}, RankingV1, 2},
		{"v2: a custom avoid is a score", Evaluation{Candidate: live, Avoided: true, CustomAvoided: true}, RankingV2, 0},
	}
	for _, c := range cases {
		if got := GrabTier(c.ranking, c.ev); got != c.want {
			t.Errorf("%s: tier %d, want %d", c.name, got, c.want)
		}
	}
}
