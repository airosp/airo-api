package cloudinary_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/airosp/airo-api/internal/platform/cloudinary"
)

func refeicoes(t *testing.T, delivery string) *cloudinary.Client {
	t.Helper()
	c, err := cloudinary.New(cloudinary.Config{
		CloudName: "savanapoint", APIKey: "k", APISecret: "s",
		Folder: "airo/meals", DeliveryBase: delivery,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const dono = "11111111-2222-3333-4444-555555555555"

func TestSoAnalisaAFotografiaDeQuemPede(t *testing.T) {
	c := refeicoes(t, "https://res.cloudinary.test")
	ok := "https://res.cloudinary.test/savanapoint/image/upload/c_fill,g_auto,q_auto,f_auto,w_1024,h_1024/v1789/airo/meals/" +
		dono + "/0123456789abcdef01234567.jpg"

	got, err := c.MealAnalysisURL(ok, dono)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://res.cloudinary.test/savanapoint/image/upload/c_limit,w_1024,h_1024,q_auto,f_jpg/v1789/airo/meals/" +
		dono + "/0123456789abcdef01234567.jpg"
	if got != want {
		t.Fatalf("\n%s\n%s", got, want)
	}

	for nome, url := range map[string]string{
		"de outra pessoa":      strings.Replace(ok, dono, "99999999-2222-3333-4444-555555555555", 1),
		"de outra conta":       strings.Replace(ok, "/savanapoint/", "/outra/", 1),
		"de outra pasta":       strings.Replace(ok, "airo/meals", "airo/profiles", 1),
		"de outro sítio":       strings.Replace(ok, "res.cloudinary.test", "mau.example", 1),
		"a subir pastas":       strings.Replace(ok, "/0123456789abcdef01234567", "/../x/0123456789abcdef01234567", 1),
		"nome que não é nosso": strings.Replace(ok, "0123456789abcdef01234567", "avatar", 1),
	} {
		if _, err := c.MealAnalysisURL(url, dono); err == nil {
			t.Errorf("%s: aceite", nome)
		}
	}
}

func TestDescarregarTemTecto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grande" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(make([]byte, cloudinary.MaxFetchBytes+10))
			return
		}
		if r.URL.Path == "/texto" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xff, 0xd8, 0xff})
	}))
	t.Cleanup(srv.Close)
	c := refeicoes(t, srv.URL)

	body, mime, err := c.Fetch(context.Background(), srv.URL+"/ok")
	if err != nil || mime != "image/jpeg" || len(body) != 3 {
		t.Fatalf("%v %s %d", err, mime, len(body))
	}
	if _, _, err := c.Fetch(context.Background(), srv.URL+"/grande"); err == nil {
		t.Error("uma imagem acima do tecto não entra")
	}
	if _, _, err := c.Fetch(context.Background(), srv.URL+"/texto"); err == nil {
		t.Error("o que não é imagem não entra")
	}
}
