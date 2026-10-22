package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSend(t *testing.T) {
	var got sendBody
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/email/send" {
			t.Errorf("caminho %s", r.URL.Path)
		}
		key = r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"recipientCount":1,"successfulEmails":[{"recipient":"a@b.co","emailId":"x"}],"failedEmails":[]}`))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "mtg_live_x", From: "Airo <noreply@savanapoint.com>", BaseURL: srv.URL})
	if err := c.Send(context.Background(), Message{To: "a@b.co", Subject: "Olá", HTML: "<p>x</p>"}); err != nil {
		t.Fatal(err)
	}
	if key != "mtg_live_x" || got.From != "Airo <noreply@savanapoint.com>" || len(got.Recipients) != 1 || got.Recipients[0] != "a@b.co" {
		t.Fatalf("pedido: %q %+v", key, got)
	}
}

func TestSendRecusado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"failedEmails":[{"recipient":"a@b.co","error":"bounced"}]}`))
	}))
	defer srv.Close()
	c := New(Config{APIKey: "k", From: "f@x.co", BaseURL: srv.URL})
	if err := c.Send(context.Background(), Message{To: "a@b.co"}); err == nil {
		t.Fatal("um destinatário recusado é um erro")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad key"}`))
	}))
	defer srv2.Close()
	if err := New(Config{APIKey: "k", From: "f@x.co", BaseURL: srv2.URL}).Send(context.Background(), Message{To: "a@b.co"}); err == nil {
		t.Fatal("401 é um erro")
	}
}
