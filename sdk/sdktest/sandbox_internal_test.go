package sdktest

import (
	"testing"

	"gotest.tools/v3/assert"
)

// A suite asking for the live cache in CI must fail when it cannot reach one,
// rather than substituting the in-memory cache and reporting a pass on a
// behaviour it never exercised.
func TestUseLiveCache(t *testing.T) {
	tests := []struct {
		name      string
		offered   bool
		mode      string
		reachable bool
		want      bool
		wantErr   string
	}{
		{
			name:      "RequestedAndReachable",
			offered:   true,
			mode:      "live",
			reachable: true,
			want:      true,
		},
		{
			name:    "RequestedAndUnreachable_IsAnError",
			offered: true,
			mode:    "live",
			wantErr: "not reachable",
		},
		{
			name:      "NotRequested_StaysInMemory",
			offered:   true,
			mode:      "",
			reachable: true,
		},
		{
			name:      "UnrecognisedMode_StaysInMemory",
			offered:   true,
			mode:      "yes-please",
			reachable: true,
		},
		{
			// A sandbox that never offered the live cache must not be switched
			// over by an env var meant for one that did.
			name:      "NotOffered_StaysInMemoryEvenWhenAsked",
			mode:      "live",
			reachable: true,
		},
		{
			name:    "NotOffered_UnreachableIsNotAnError",
			mode:    "live",
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := useLiveCache(tt.offered, tt.mode, func() bool { return tt.reachable })

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Assert(t, !got, "must not report the live cache as usable when it errored")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

// Reachability must not be probed unless the live cache was actually asked for.
func TestUseLiveCache_DoesNotProbeUnlessAsked(t *testing.T) {
	probed := false
	reachable := func() bool {
		probed = true
		return true
	}

	_, err := useLiveCache(false, "", reachable)
	assert.NilError(t, err)
	assert.Assert(t, !probed, "expected no reachability probe when the live cache was not offered")

	_, err = useLiveCache(true, "", reachable)
	assert.NilError(t, err)
	assert.Assert(t, !probed, "expected no reachability probe when live mode was not requested")
}
