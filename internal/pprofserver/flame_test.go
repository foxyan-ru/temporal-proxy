package pprofserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPickValueIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		types  []valueType
		prefer string
		want   int
	}{
		{
			name:  "no types falls back to zero",
			types: nil,
			want:  0,
		},
		{
			name: "the heap view prefers its in-use space",
			types: []valueType{
				{name: "alloc_objects"}, {name: "alloc_space"},
				{name: "inuse_objects"}, {name: "inuse_space"},
			},
			prefer: "inuse_space",
			want:   3,
		},
		{
			name: "the allocated view prefers its allocated space",
			types: []valueType{
				{name: "alloc_objects"}, {name: "alloc_space"},
				{name: "inuse_objects"}, {name: "inuse_space"},
			},
			prefer: "alloc_space",
			want:   1,
		},
		{
			name:  "the last type wins when nothing is preferred",
			types: []valueType{{name: "objects"}, {name: "space"}},
			want:  1,
		},
		{
			name:  "cpu time wins over samples even without a preference",
			types: []valueType{{name: "samples"}, {name: "time"}},
			want:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, pickValueIndex(tc.types, tc.prefer))
		})
	}
}

func TestBuildFlame(t *testing.T) {
	t.Parallel()

	prof, err := parseProfile(testProfile(t))
	require.NoError(t, err)

	tree := buildFlame(prof, "count")
	require.Equal(t, int64(8), tree.total)
	require.Equal(t, "count", tree.valueName)
	require.Equal(t, countValue, tree.kind)
	require.Equal(t, int64(8), tree.root.total)
	require.Equal(t, int64(0), tree.root.self)

	main, ok := tree.root.children["main"]
	require.True(t, ok, "main should be a child of root")
	require.Equal(t, int64(8), main.total)
	require.Equal(t, int64(3), main.self)

	worker, ok := main.children["worker.run"]
	require.True(t, ok, "worker.run should sit under main")
	require.Equal(t, int64(5), worker.total)
	require.Equal(t, int64(5), worker.self)
	require.Empty(t, worker.children)

	require.Equal(t, 2, tree.maxDepth)
}

func TestBuildFlameIgnoresZeroValues(t *testing.T) {
	t.Parallel()

	// A heap profile's samples carry every type; the samples that hold nothing
	// in the chosen value contribute nothing to that view.
	prof := &profileData{
		types: []valueType{{name: "alloc_space", unit: "bytes"}, {name: "inuse_space", unit: "bytes"}},
		samples: []sampleInfo{
			{stack: []string{"leaf", "caller"}, values: []int64{100, 0}},
			{stack: []string{"caller"}, values: []int64{50, 0}},
		},
	}

	tree := buildFlame(prof, "inuse_space")
	require.Equal(t, int64(0), tree.total, "inuse_space, the preferred type, is all zero")
	require.Empty(t, tree.root.children)
}

func TestTop(t *testing.T) {
	t.Parallel()

	prof, err := parseProfile(testProfile(t))
	require.NoError(t, err)

	tree := buildFlame(prof, "count")
	stats := tree.top(25)
	require.Len(t, stats, 3) // root, main, worker.run

	byName := map[string]frameStat{}
	for _, s := range stats {
		byName[s.name] = s
	}

	// root and main tie at 8, so the sort order between them is unspecified;
	// the totals and self values are what matter.
	require.Equal(t, int64(8), byName["root"].total)
	require.Equal(t, int64(8), byName["main"].total)
	require.Equal(t, int64(3), byName["main"].self)
	require.Equal(t, int64(5), byName["worker.run"].total)
	require.Equal(t, int64(5), byName["worker.run"].self)
}

func TestFormat(t *testing.T) {
	t.Parallel()

	tree := &flameTree{kind: countValue}
	require.Equal(t, "950", tree.format(950))
	require.Equal(t, "1.50 k", tree.format(1500))
	require.Equal(t, "2.00 M", tree.format(2_000_000))

	tree = &flameTree{kind: timeValue}
	require.Equal(t, "500 ns", tree.format(500))
	require.Equal(t, "1.50 ms", tree.format(1_500_000))
	require.Equal(t, "2.00 s", tree.format(2_000_000_000))

	tree = &flameTree{kind: byteValue}
	require.Equal(t, "512 B", tree.format(512))
	require.Equal(t, "1.50 KiB", tree.format(1536))
	require.Equal(t, "3.00 MiB", tree.format(3<<20))
}

func TestSVGEscapesFrameNames(t *testing.T) {
	t.Parallel()

	prof := &profileData{
		types:   []valueType{{name: "count", unit: "count"}},
		samples: []sampleInfo{{stack: []string{`main<script>alert("x")</script>`}, values: []int64{1}}},
	}

	tree := buildFlame(prof, "count")
	svg := tree.svg()
	require.Contains(t, svg, `main<script>`)
	require.NotContains(t, svg, "<script>")
}
