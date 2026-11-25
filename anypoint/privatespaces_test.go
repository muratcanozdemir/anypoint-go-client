package anypoint

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransitGatewayDetached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"attachment is not attached to the private space"}`)
	}))
	defer srv.Close()
	_, err := NewClient(srv.URL, WithToken("tok")).GetTransitGateway(t.Context(), "o", "ps", "tgw")
	if !errors.Is(err, ErrTransitGatewayDetached) || IsNotFound(err) {
		t.Errorf("a detached attachment: %v", err)
	}
}

func TestAWSTransitGatewayID(t *testing.T) {
	for raw, want := range map[string]string{
		"https://console.aws.amazon.com/vpc/home?region=us-east-2#TransitGatewayDetails:transitGatewayId=tgw-0abc": "tgw-0abc",
		"https://console.aws.amazon.com/vpc/home?transitGatewayId=tgw-1&region=x":                                  "tgw-1",
		"tgw-2":                       "tgw-2",
		"":                            "",
		"https://example.com/unknown": "https://example.com/unknown",
	} {
		if got := (TransitGatewayStatus{TGWResource: raw}).AWSTransitGatewayID(); got != want {
			t.Errorf("%q: got %q, want %q", raw, got, want)
		}
	}
}
