package worker

import (
	"testing"

	"github.com/dada-tuda/console/build-agent/internal/db"
)

func TestReconcilable(t *testing.T) {
	num := 17
	var queue int64 = 5897
	cases := []struct {
		name string
		rb   db.ReclaimBuild
		want bool
	}{
		{"has build number", db.ReclaimBuild{JenkinsBuildNumber: &num}, true},
		{"has queue id only", db.ReclaimBuild{JenkinsQueueID: &queue}, true},
		{"has both", db.ReclaimBuild{JenkinsBuildNumber: &num, JenkinsQueueID: &queue}, true},
		{"no jenkins ref", db.ReclaimBuild{}, false},
	}
	for _, tc := range cases {
		if got := reconcilable(tc.rb); got != tc.want {
			t.Errorf("%s: reconcilable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
