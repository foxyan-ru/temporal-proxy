package pprofserver

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"

	"google.golang.org/protobuf/encoding/protowire"
)

// This parser reads the subset of the pprof wire format (profile.proto) the
// visualizer needs: the string table, the sample types, the samples, and the
// functions and locations naming each frame. Unknown fields are skipped, so a
// profile written by a newer writer still parses, and a gzip header is
// honored because the runtime wraps CPU profiles in one.

var errMalformed = errors.New("malformed profile")

// Profile message field numbers.
const (
	profileSampleType  = 1 // repeated ValueType
	profileSample      = 2 // repeated Sample
	profileLocation    = 4 // repeated Location
	profileFunction    = 5 // repeated Function
	profileStringTable = 6 // repeated string
	valueTypeType      = 1
	valueTypeUnit      = 2
	sampleLocationID   = 1 // repeated uint64, packed or not
	sampleValue        = 2 // repeated int64, packed or not
	locationID         = 1
	locationLine       = 4 // repeated Line
	lineFunctionID     = 1
	functionID         = 1
	functionName       = 2
)

type (
	// valueType names one sample value and its unit, both as string table
	// entries resolved after parsing.
	valueType struct{ name, unit string }

	// profileData is a parsed profile: every sample's expanded frame stack and
	// its values, aligned with types.
	profileData struct {
		types   []valueType
		samples []sampleInfo
	}

	// sampleInfo carries one sample. Stack is leaf-first, the order the wire
	// format lists locations in, so stack[0] is the innermost frame.
	sampleInfo struct {
		stack  []string
		values []int64
	}
)

// parseProfile decodes a profile body, gzipped or not.
func parseProfile(body []byte) (*profileData, error) {
	raw, err := gzip.NewReader(bytes.NewReader(body))
	if err == nil {
		body, err = io.ReadAll(raw)
		if err != nil {
			return nil, errors.Join(errMalformed, err)
		}
	}

	r, err := parseRaw(body)
	if err != nil {
		return nil, err
	}

	return r.resolve(), nil
}

// rawProfile is what the wire format actually carries: indices into the string
// table and ids, which only resolve once every message has been read, since a
// writer may put the string table last.
type rawProfile struct {
	strings   []string
	types     []rawValueType
	functions map[uint64]rawFunction
	locations map[uint64]rawLocation
	samples   []rawSample
}

type (
	rawValueType struct{ typeName, unitName int64 }
	rawFunction  struct{ name int64 }
	rawLocation  struct{ lines []rawLine }
	rawLine      struct{ functionID uint64 }
	rawSample    struct {
		locationIDs []uint64
		values      []int64
	}
)

func parseRaw(b []byte) (*rawProfile, error) {
	r := &rawProfile{
		functions: map[uint64]rawFunction{},
		locations: map[uint64]rawLocation{},
	}

	fields, err := splitFields(b)
	if err != nil {
		return nil, err
	}

	for _, f := range fields {
		switch {
		case f.num == profileStringTable && f.typ == protowire.BytesType:
			r.strings = append(r.strings, string(f.val))
		case f.num == profileSampleType && f.typ == protowire.BytesType:
			vt, err := parseValueType(f.val)
			if err != nil {
				return nil, err
			}

			r.types = append(r.types, vt)
		case f.num == profileSample && f.typ == protowire.BytesType:
			s, err := parseSample(f.val)
			if err != nil {
				return nil, err
			}

			r.samples = append(r.samples, s)
		case f.num == profileFunction && f.typ == protowire.BytesType:
			fn, id, err := parseFunction(f.val)
			if err != nil {
				return nil, err
			}

			r.functions[id] = fn
		case f.num == profileLocation && f.typ == protowire.BytesType:
			loc, id, err := parseLocation(f.val)
			if err != nil {
				return nil, err
			}

			r.locations[id] = loc
		}
	}

	return r, nil
}

// resolve turns the raw indices into names. A reference the writer never
// defined resolves to "?", which reads better than failing a profile that
// names one frame unusually.
func (r *rawProfile) resolve() *profileData {
	str := func(i int64) string {
		if i >= 0 && int(i) < len(r.strings) {
			return r.strings[i]
		}

		return "?"
	}

	p := &profileData{}
	for _, t := range r.types {
		p.types = append(p.types, valueType{name: str(t.typeName), unit: str(t.unitName)})
	}

	for _, s := range r.samples {
		si := sampleInfo{values: s.values}
		for _, id := range s.locationIDs {
			loc, ok := r.locations[id]
			if !ok {
				si.stack = append(si.stack, "?")

				continue
			}

			for _, line := range loc.lines {
				fn, ok := r.functions[line.functionID]
				if !ok {
					si.stack = append(si.stack, "?")

					continue
				}

				si.stack = append(si.stack, str(fn.name))
			}
		}

		p.samples = append(p.samples, si)
	}

	return p
}

// field is one message field: its number, wire type, and the varint or content
// it carries. Fields the parser does not care about still arrive here, with
// their value filled in by wire type.
type field struct {
	num protowire.Number
	typ protowire.Type
	val []byte
	v   uint64
}

// splitFields walks one message and returns its fields. It accepts any wire
// type, skipping the fixed-width ones, so a writer using an encoding this
// parser does not interpret still parses through. Truncated input is an error.
func splitFields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, errMalformed
		}

		b = b[n:]
		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, errMalformed
			}

			out = append(out, field{num: num, typ: typ, v: v})
			b = b[n:]
		case protowire.BytesType:
			val, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil, errMalformed
			}

			out = append(out, field{num: num, typ: typ, val: val})
			b = b[n:]
		case protowire.Fixed32Type:
			_, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return nil, errMalformed
			}

			b = b[n:]
		case protowire.Fixed64Type:
			_, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return nil, errMalformed
			}

			b = b[n:]
		default:
			return nil, errMalformed
		}
	}

	return out, nil
}

// unpackInts reads a field's payload as a run of varints, which is how a
// packed repeated integer field arrives, and also accepts a single varint's
// payload for writers that did not pack the field.
func unpackInts(f field) ([]uint64, error) {
	if f.typ == protowire.VarintType {
		return []uint64{f.v}, nil
	}

	var out []uint64
	for len(f.val) > 0 {
		v, n := protowire.ConsumeVarint(f.val)
		if n < 0 {
			return nil, errMalformed
		}

		out = append(out, v)
		f.val = f.val[n:]
	}

	return out, nil
}

func parseValueType(b []byte) (rawValueType, error) {
	vt := rawValueType{typeName: -1, unitName: -1}
	fields, err := splitFields(b)
	if err != nil {
		return vt, err
	}

	for _, f := range fields {
		switch f.num {
		case valueTypeType:
			vt.typeName = int64(f.v)
		case valueTypeUnit:
			vt.unitName = int64(f.v)
		}
	}

	return vt, nil
}

func parseSample(b []byte) (rawSample, error) {
	s := rawSample{}
	fields, err := splitFields(b)
	if err != nil {
		return s, err
	}

	for _, f := range fields {
		switch f.num {
		case sampleLocationID:
			ids, err := unpackInts(f)
			if err != nil {
				return s, err
			}

			s.locationIDs = append(s.locationIDs, ids...)
		case sampleValue:
			vals, err := unpackInts(f)
			if err != nil {
				return s, err
			}

			for _, v := range vals {
				s.values = append(s.values, int64(v))
			}
		}
	}

	return s, nil
}

func parseFunction(b []byte) (rawFunction, uint64, error) {
	fn := rawFunction{name: -1}
	id := uint64(0)
	fields, err := splitFields(b)
	if err != nil {
		return fn, 0, err
	}

	for _, f := range fields {
		switch f.num {
		case functionID:
			id = f.v
		case functionName:
			fn.name = int64(f.v)
		}
	}

	if id == 0 {
		return fn, 0, errMalformed
	}

	return fn, id, nil
}

func parseLocation(b []byte) (rawLocation, uint64, error) {
	loc := rawLocation{}
	id := uint64(0)
	fields, err := splitFields(b)
	if err != nil {
		return loc, 0, err
	}

	for _, f := range fields {
		switch f.num {
		case locationID:
			id = f.v
		case locationLine:
			lines, err := splitFields(f.val)
			if err != nil {
				return loc, 0, err
			}

			for _, lf := range lines {
				if lf.num == lineFunctionID {
					loc.lines = append(loc.lines, rawLine{functionID: lf.v})
				}
			}
		}
	}

	if id == 0 {
		return loc, 0, errMalformed
	}

	return loc, id, nil
}
