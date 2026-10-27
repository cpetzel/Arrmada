package quality

import "testing"

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
