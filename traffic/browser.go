package traffic

import (
	"errors"
	"net/http"

	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

// RecordBrowser stores an exchange already authorized and sent by the Go browser
// relay. Reusing the recorder avoids a second DNS lookup or a fail-open MITM path
// while retaining target Host, bodies, cookies, and the existing traffic tools.
func (t *Traffic) RecordBrowser(request *http.Request, requestBody []byte, response *http.Response, responseBody []byte) error {
	if request == nil || request.URL == nil || response == nil {
		return errors.New("브라우저 트래픽 요청/응답이 없습니다")
	}
	headers := request.Header.Clone()
	headers.Set("Host", request.URL.Host)
	return t.record(&mproxy.Flow{
		Request:  &mproxy.Request{Method: request.Method, URL: request.URL, Proto: "HTTP/1.1", Header: headers, Body: requestBody},
		Response: &mproxy.Response{StatusCode: response.StatusCode, Header: response.Header, Body: responseBody},
	})
}
