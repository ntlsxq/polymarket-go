package clob

import (
	"strconv"

	"github.com/goccy/go-json"
)

// flexInt64 decodes a JSON value that may arrive as either a number or a
// numeric string. Polymarket's APIs are inconsistent on numeric fields
// (base_fee in particular), so a straight int64 unmarshal fails on the
// string variant.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		*f = flexInt64(v)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexInt64(int64(n))
	return nil
}

// flexFloat64 mirrors flexInt64 for fields that may be a JSON number or a
// numeric string (e.g. feeSchedule.feeRate, orderPriceMinTickSize).
type flexFloat64 float64

func (f *flexFloat64) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			return nil
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = flexFloat64(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*f = flexFloat64(v)
	return nil
}

// flexString accepts a JSON string OR number and renders the value as its
// string form. Used for fields like minimum_tick_size that ship as either
// "0.01" or 0.01 depending on the endpoint.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexString(strconv.FormatFloat(n, 'f', -1, 64))
	return nil
}
