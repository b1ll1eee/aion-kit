package idempotency

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/http"
)

// recorder passes the response through to the client while keeping a copy
// to store.
type recorder struct {
	http.ResponseWriter
	code   int
	header http.Header // snapshot taken at WriteHeader
	body   *bytes.Buffer
}

func newRecorder(w http.ResponseWriter) *recorder {
	return &recorder{ResponseWriter: w, body: bufPool.Get().(*bytes.Buffer)}
}

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
		r.header = r.Header().Clone()
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.code == 0 {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(p)
	return r.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) status() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// Wire format, version 1:
//
//	0x01 | uvarint status | uvarint nheaders |
//	  { str name | uvarint nvalues | { str value } } | body
//
// where str is uvarint length followed by bytes.
const respVersion = 1

var errBadResponse = errors.New("idempotency: corrupt stored response")

func (r *recorder) encode() []byte {
	if r.header == nil { // handler never wrote; headers go out at return
		r.header = r.Header().Clone()
	}
	n := 1 + 2*binary.MaxVarintLen64 + r.body.Len()
	for k, vs := range r.header {
		n += len(k) + 2*binary.MaxVarintLen64
		for _, v := range vs {
			n += len(v) + binary.MaxVarintLen64
		}
	}
	b := make([]byte, 0, n)
	b = append(b, respVersion)
	b = binary.AppendUvarint(b, uint64(r.status()))
	b = binary.AppendUvarint(b, uint64(len(r.header)))
	for k, vs := range r.header {
		b = appendStr(b, k)
		b = binary.AppendUvarint(b, uint64(len(vs)))
		for _, v := range vs {
			b = appendStr(b, v)
		}
	}
	return append(b, r.body.Bytes()...)
}

func appendStr(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func decodeResponse(b []byte) (response, error) {
	d := decoder{b: b}
	if v := d.byte(); v != respVersion {
		return response{}, errBadResponse
	}
	resp := response{status: int(d.uvarint())}
	nh := d.uvarint()
	if d.err == nil && nh > uint64(len(d.b)) {
		d.err = errBadResponse
	}
	if d.err == nil {
		resp.header = make(http.Header, nh)
	}
	for i := uint64(0); i < nh && d.err == nil; i++ {
		k := d.str()
		nv := d.uvarint()
		if nv > uint64(len(d.b)) {
			d.err = errBadResponse
			break
		}
		vs := make([]string, 0, nv)
		for j := uint64(0); j < nv && d.err == nil; j++ {
			vs = append(vs, d.str())
		}
		resp.header[k] = vs
	}
	if d.err != nil || resp.status < 100 || resp.status > 999 {
		return response{}, errBadResponse
	}
	resp.body = d.b
	return resp, nil
}

type decoder struct {
	b   []byte
	err error
}

func (d *decoder) byte() byte {
	if d.err != nil || len(d.b) == 0 {
		d.err = errBadResponse
		return 0
	}
	c := d.b[0]
	d.b = d.b[1:]
	return c
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.err = errBadResponse
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *decoder) str() string {
	n := d.uvarint()
	if d.err != nil || n > uint64(len(d.b)) {
		d.err = errBadResponse
		return ""
	}
	s := string(d.b[:n])
	d.b = d.b[n:]
	return s
}
