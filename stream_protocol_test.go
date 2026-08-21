package fun

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

type ProtocolStreamSvc struct{}

func (*ProtocolStreamSvc) Empty() (*Stream, error) {
	stream := &Stream{}
	go stream.Close()
	return stream, nil
}

func (*ProtocolStreamSvc) Before() (*Stream, error) {
	return nil, errors.New("before stream")
}

func (*ProtocolStreamSvc) Business() (*Stream, error) {
	return nil, Error(4201, "business before stream")
}

func serveFun(t *testing.T, f *Fun) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- fasthttp.Serve(listener, f.handle) }()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return "http://" + listener.Addr().String()
}

func streamPost(t *testing.T, url, method string) (*http.Response, []byte) {
	t.Helper()
	body := strings.NewReader(`{"serviceName":"ProtocolStreamSvc","methodName":"` + method + `"}`)
	request, err := http.NewRequest(http.MethodPost, url+"/cell", body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Close = true
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestServerStreamContentTypeAndEmptyStream(t *testing.T) {
	f := New()
	f.BindService(&ProtocolStreamSvc{})
	response, body := streamPost(t, serveFun(t, f), "Empty")
	if got := response.Header.Get("Content-Type"); got != "application/x-ndjson" {
		t.Fatalf("Content-Type = %q", got)
	}
	if len(body) != 0 {
		t.Fatalf("empty stream returned %q", body)
	}
}

func TestServerStreamSetupErrorsUseResultProtocol(t *testing.T) {
	f := New()
	f.BindService(&ProtocolStreamSvc{})
	url := serveFun(t, f)
	for _, test := range []struct {
		method string
		status uint8
		code   uint16
		msg    string
	}{
		{method: "Before", status: 1, msg: "before stream"},
		{method: "Business", status: 2, code: 4201, msg: "business before stream"},
	} {
		t.Run(test.method, func(t *testing.T) {
			response, body := streamPost(t, url, test.method)
			if strings.HasPrefix(response.Header.Get("Content-Type"), "application/x-ndjson") {
				t.Fatalf("setup error used stream Content-Type: %q", response.Header.Get("Content-Type"))
			}
			var result Result[any]
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatalf("invalid Result body %q: %v", body, err)
			}
			if result.Status != test.status || result.Msg == nil || *result.Msg != test.msg {
				t.Fatalf("unexpected Result: %+v", result)
			}
			if test.code != 0 && (result.Code == nil || *result.Code != test.code) {
				t.Fatalf("code = %v, want %d", result.Code, test.code)
			}
		})
	}
}
