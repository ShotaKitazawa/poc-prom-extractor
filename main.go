package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	remoteReadUrl  = "http://localhost:19090/api/v1/read"
	remoteWriteUrl = "http://localhost:29090/api/v1/write"
)

func main() {
	matcher := `{__name__=~"process_cpu_seconds_total"}`

	ms, err := parser.ParseExpr(matcher)
	if err != nil {
		panic(err)
	}

	vs, ok := ms.(*parser.VectorSelector)
	if !ok {
		panic("cannot cast")
	}

	labelMatchers, err := toLabelMatchers(vs.LabelMatchers)
	if err != nil {
		panic(err)
	}

	ticker := time.NewTicker(3 * time.Second)
	base := time.Now()

	for {
		select {
		case <-ticker.C:
			func() {
				now := time.Now()
				defer func() {
					base = now
				}()

				//
				// remote read
				//
				var readReqBody prompb.ReadRequest
				readReqBody.Queries = []*prompb.Query{
					{
						StartTimestampMs: base.UnixMilli(),
						EndTimestampMs:   now.UnixMilli(),
						Matchers:         labelMatchers,
					},
				}
				compressed, err := encode(&readReqBody)
				if err != nil {
					panic(err)
				}
				req, err := http.NewRequest("POST", remoteReadUrl, bytes.NewBuffer(compressed))
				if err != nil {
					panic(err)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					panic(err)
				}
				compressed, err = ioutil.ReadAll(resp.Body)
				if err != nil {
					panic(err)
				}
				respBuf, err := snappy.Decode(nil, compressed)
				if err != nil {
					panic(err)
				}
				var readRespBody prompb.ReadResponse
				if err := proto.Unmarshal(respBuf, &readRespBody); err != nil {
					panic(err)
				}

				//
				// remote write
				//
				for _, result := range readRespBody.Results {
					var writeReqBody prompb.WriteRequest
					for _, ts := range result.Timeseries {
						writeReqBody.Timeseries = append(writeReqBody.Timeseries, *ts)
					}
					compressed, err = encode(&writeReqBody)
					if err != nil {
						panic(err)
					}
					req, err := http.NewRequest("POST", remoteWriteUrl, bytes.NewBuffer(compressed))
					if err != nil {
						panic(err)
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						panic(err)
					}
					fmt.Println(resp.StatusCode)
				}
			}()
		}
	}

}

func toLabelMatchers(matchers []*labels.Matcher) ([]*prompb.LabelMatcher, error) {
	pbMatchers := make([]*prompb.LabelMatcher, 0, len(matchers))
	for _, m := range matchers {
		var mType prompb.LabelMatcher_Type
		switch m.Type {
		case labels.MatchEqual:
			mType = prompb.LabelMatcher_EQ
		case labels.MatchNotEqual:
			mType = prompb.LabelMatcher_NEQ
		case labels.MatchRegexp:
			mType = prompb.LabelMatcher_RE
		case labels.MatchNotRegexp:
			mType = prompb.LabelMatcher_NRE
		default:
			return nil, fmt.Errorf("invalid matcher type")
		}
		pbMatchers = append(pbMatchers, &prompb.LabelMatcher{
			Type:  mType,
			Name:  m.Name,
			Value: m.Value,
		})
	}
	return pbMatchers, nil
}

func encode(req proto.Message) ([]byte, error) {
	buf, err := proto.Marshal(req)
	if err != nil {
		return nil, err
	}
	return snappy.Encode(nil, buf), nil
}
