package pprofserver

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// testProfile builds a small profile by hand: two functions, two locations,
// and two samples. The string table is [ "", "worker.run", "bytes", "count",
// "main" ], so type "count" (index 3) with unit "count" (index 3) is the only
// sample type, and function "main" (index 4) calls "worker.run" (index 1).
//
// Sample one walks worker.run (leaf, location 11) under main (location 10)
// with value 5. Sample two is main alone with value 3.
func testProfile(t *testing.T) []byte {
	t.Helper()

	var b []byte
	strTable := func(entries ...string) {
		for _, s := range entries {
			b = protowire.AppendTag(b, profileStringTable, protowire.BytesType)
			b = protowire.AppendString(b, s)
		}
	}

	valueTypeMsg := func(nameIdx, unitIdx int64) {
		b = protowire.AppendTag(b, profileSampleType, protowire.BytesType)
		var vt []byte
		vt = protowire.AppendTag(vt, valueTypeType, protowire.VarintType)
		vt = protowire.AppendVarint(vt, uint64(nameIdx))
		vt = protowire.AppendTag(vt, valueTypeUnit, protowire.VarintType)
		vt = protowire.AppendVarint(vt, uint64(unitIdx))
		b = protowire.AppendBytes(b, vt)
	}

	functionMsg := func(id uint64, nameIdx int64) {
		b = protowire.AppendTag(b, profileFunction, protowire.BytesType)
		var fn []byte
		fn = protowire.AppendTag(fn, functionID, protowire.VarintType)
		fn = protowire.AppendVarint(fn, id)
		fn = protowire.AppendTag(fn, functionName, protowire.VarintType)
		fn = protowire.AppendVarint(fn, uint64(nameIdx))
		b = protowire.AppendBytes(b, fn)
	}

	locationMsg := func(id uint64, functionIDs ...uint64) {
		b = protowire.AppendTag(b, profileLocation, protowire.BytesType)
		var loc []byte
		loc = protowire.AppendTag(loc, locationID, protowire.VarintType)
		loc = protowire.AppendVarint(loc, id)
		for _, fid := range functionIDs {
			loc = protowire.AppendTag(loc, locationLine, protowire.BytesType)
			var line []byte
			line = protowire.AppendTag(line, lineFunctionID, protowire.VarintType)
			line = protowire.AppendVarint(line, fid)
			loc = protowire.AppendBytes(loc, line)
		}

		b = protowire.AppendBytes(b, loc)
	}

	sampleMsg := func(locationIDs []uint64, values []int64) {
		b = protowire.AppendTag(b, profileSample, protowire.BytesType)
		var s []byte
		for _, id := range locationIDs {
			s = protowire.AppendTag(s, sampleLocationID, protowire.VarintType)
			s = protowire.AppendVarint(s, id)
		}

		for _, v := range values {
			s = protowire.AppendTag(s, sampleValue, protowire.VarintType)
			s = protowire.AppendVarint(s, uint64(v))
		}

		b = protowire.AppendBytes(b, s)
	}

	strTable("", "worker.run", "bytes", "count", "main")
	valueTypeMsg(3, 3)
	functionMsg(1, 4) // main
	functionMsg(2, 1) // worker.run
	locationMsg(10, 1)
	locationMsg(11, 2)
	sampleMsg([]uint64{11, 10}, []int64{5}) // worker.run under main
	sampleMsg([]uint64{10}, []int64{3})     // main alone

	return b
}

func TestParseProfile(t *testing.T) {
	t.Parallel()

	prof, err := parseProfile(testProfile(t))
	require.NoError(t, err)

	require.Len(t, prof.types, 1)
	require.Equal(t, "count", prof.types[0].name)
	require.Equal(t, "count", prof.types[0].unit)

	require.Len(t, prof.samples, 2)
	// Leaf-first, as written: worker.run under main.
	require.Equal(t, []string{"worker.run", "main"}, prof.samples[0].stack)
	require.Equal(t, []int64{5}, prof.samples[0].values)
	require.Equal(t, []string{"main"}, prof.samples[1].stack)
	require.Equal(t, []int64{3}, prof.samples[1].values)
}

func TestParseProfileGzipped(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, err := w.Write(testProfile(t))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	prof, err := parseProfile(buf.Bytes())
	require.NoError(t, err)
	require.Len(t, prof.samples, 2)
}

func TestParseProfileMalformed(t *testing.T) {
	t.Parallel()

	// A truncated submessage: a length prefix promising more bytes than exist.
	body := []byte{0x12, 0x40, 0x0a}
	_, err := parseProfile(body)
	require.ErrorIs(t, err, errMalformed)
}

func TestParseProfileUnknownReferences(t *testing.T) {
	t.Parallel()

	// A sample naming a location the writer never sent, and a function whose
	// name index points past the string table, both resolve to "?" instead of
	// failing the whole profile.
	var b []byte
	b = protowire.AppendTag(b, profileStringTable, protowire.BytesType)
	b = protowire.AppendString(b, "")
	b = protowire.AppendTag(b, profileStringTable, protowire.BytesType)
	b = protowire.AppendString(b, "count")
	b = protowire.AppendTag(b, profileSampleType, protowire.BytesType)
	var vt []byte
	vt = protowire.AppendTag(vt, valueTypeType, protowire.VarintType)
	vt = protowire.AppendVarint(vt, 1)
	b = protowire.AppendBytes(b, vt)
	b = protowire.AppendTag(b, profileSample, protowire.BytesType)
	var s []byte
	s = protowire.AppendTag(s, sampleLocationID, protowire.VarintType)
	s = protowire.AppendVarint(s, 99)
	s = protowire.AppendTag(s, sampleValue, protowire.VarintType)
	s = protowire.AppendVarint(s, 7)
	b = protowire.AppendBytes(b, s)

	prof, err := parseProfile(b)
	require.NoError(t, err)
	require.Len(t, prof.samples, 1)
	require.Equal(t, []string{"?"}, prof.samples[0].stack)
}
