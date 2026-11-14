package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smarty/gunit"
	"github.com/smarty/gunit/assert/should"

	"github.com/smartybryan/steal-some-christmas-cheer/internal/auth"
	"github.com/smartybryan/steal-some-christmas-cheer/internal/store"
)

func TestServerFixture(t *testing.T) {
	gunit.Run(new(ServerFixture), t)
}

type ServerFixture struct {
	*gunit.Fixture
	store    *store.Store
	handler  http.Handler
	notifier *fakeNotifier
	mailer   *fakeMailer
	now      time.Time
}

type fakeMailer struct {
	mu sync.Mutex
	to []string
}

func (f *fakeMailer) SendTo(_ context.Context, to, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.to = append(f.to, to)
	return nil
}

// waitForMail waits for the asynchronous registration email to be sent.
func (f *fakeMailer) waitForMail() []string {
	for range 100 {
		f.mu.Lock()
		got := slices.Clone(f.to)
		f.mu.Unlock()
		if len(got) > 0 {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

type fakeNotifier struct {
	mu     sync.Mutex
	bodies []string
}

func (f *fakeNotifier) Notify(_ context.Context, _, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, body)
	return nil
}

func (this *ServerFixture) Setup() {
	st, err := store.Open(filepath.Join(this.T().(*testing.T).TempDir(), "duck.db"))
	this.So(err, should.BeNil)
	this.store = st
	this.notifier = &fakeNotifier{}
	this.mailer = &fakeMailer{}
	this.now = time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC)
	srv := New(Config{
		OwnerPassword:  "jingle",
		Signer:         auth.NewSigner([]byte("test-secret")),
		AllowedOrigins: []string{"https://smartybryan.github.io"},
		PublicURL:      "https://api.example.com",
		SiteURL:        "https://smartybryan.github.io/steal-some-christmas-cheer",
		Email:          this.mailer,
		EmailFallback:  "fallback@example.com",
		Now:            func() time.Time { return this.now },
	}, st, this.notifier)
	this.handler = srv.Handler()
}

func (this *ServerFixture) Teardown() { _ = this.store.Close() }

func (this *ServerFixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	this.handler.ServeHTTP(rec, req)
	return rec
}

func (this *ServerFixture) register(name, street string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("name", name)
	_ = w.WriteField("street", street)
	_ = w.WriteField("city", "Springfield")
	_ = w.WriteField("message", "Got it!")
	part, _ := w.CreateFormFile("photo", "duck.png")
	_ = png.Encode(part, image.NewRGBA(image.Rect(0, 0, 20, 10)))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/register", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.RemoteAddr = "10.0.0.1:1234"
	return this.do(req)
}

func (this *ServerFixture) login() string {
	rec := this.do(httptest.NewRequest(http.MethodPost, "/api/owner/login", strings.NewReader(`{"password":"jingle"}`)))
	this.So(rec.Code, should.Equal, http.StatusOK)
	var res struct{ Token string }
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	return res.Token
}

func (this *ServerFixture) review(token string, id int, action string) int {
	req := httptest.NewRequest(http.MethodPost, "/api/owner/registrations/"+itoa(id), strings.NewReader(`{"action":"`+action+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	return this.do(req).Code
}

type publicResponse struct {
	Stops []struct {
		Name     string
		Location string
		PhotoURL string
	}
	TotalSteals int
}

func (this *ServerFixture) public() publicResponse {
	rec := this.do(httptest.NewRequest(http.MethodGet, "/api/public", nil))
	this.So(rec.Code, should.Equal, http.StatusOK)
	var res publicResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	return res
}

func itoa(i int) string { return strconv.Itoa(i) }

func (this *ServerFixture) TestFullHeistFlow() {
	this.So(this.register("The Elves", "12 Candy Cane Ln").Code, should.Equal, http.StatusCreated)
	this.So(this.register("The Reindeer", "34 Sleigh St").Code, should.Equal, http.StatusCreated)

	token := this.login()
	this.So(this.review(token, 1, "approve"), should.Equal, http.StatusOK)
	this.So(this.review(token, 2, "approve"), should.Equal, http.StatusOK)

	res := this.public()
	this.So(res.TotalSteals, should.Equal, 2)
	this.So(res.Stops, should.HaveLength, 1)
	this.So(res.Stops[0].Name, should.Equal, "The Elves")
	this.So(res.Stops[0].Location, should.Equal, "Candy Cane Ln, Springfield")
	this.So(res.Stops[0].PhotoURL, should.Equal, "https://api.example.com/api/photos/1")

	this.So(this.do(httptest.NewRequest(http.MethodGet, "/api/photos/1", nil)).Code, should.Equal, http.StatusOK)
	this.So(this.do(httptest.NewRequest(http.MethodGet, "/api/photos/2", nil)).Code, should.Equal, http.StatusNotFound)
}

func (this *ServerFixture) TestPublicResponseNeverContainsHouseNumbers() {
	this.register("The Elves", "12 Candy Cane Ln")
	this.register("The Reindeer", "34 Sleigh St")
	token := this.login()
	this.review(token, 1, "approve")

	rec := this.do(httptest.NewRequest(http.MethodGet, "/api/public", nil))
	this.So(rec.Body.String(), should.NotContainSubstring, "12 Candy")
	this.So(rec.Body.String(), should.NotContainSubstring, "Sleigh")
	this.So(rec.Body.String(), should.NotContainSubstring, "Reindeer")
}

func (this *ServerFixture) TestOwnerEndpointsRequireLogin() {
	rec := this.do(httptest.NewRequest(http.MethodGet, "/api/owner/registrations", nil))
	this.So(rec.Code, should.Equal, http.StatusUnauthorized)

	rec = this.do(httptest.NewRequest(http.MethodPost, "/api/owner/login", strings.NewReader(`{"password":"wrong"}`)))
	this.So(rec.Code, should.Equal, http.StatusUnauthorized)
}

func (this *ServerFixture) TestRegistrationNotifiesOwnerWithWorkingReviewLink() {
	this.register("The Elves", "12 Candy Cane Ln")
	var body string
	for range 100 {
		this.notifier.mu.Lock()
		if len(this.notifier.bodies) > 0 {
			body = this.notifier.bodies[0]
		}
		this.notifier.mu.Unlock()
		if body != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	this.So(body, should.ContainSubstring, "12 Candy Cane Ln")

	start := strings.Index(body, "https://api.example.com/review/")
	this.So(start, should.BeGreaterThan, -1)
	link, err := url.Parse(strings.Fields(body[start:])[0])
	this.So(err, should.BeNil)

	page := this.do(httptest.NewRequest(http.MethodGet, link.RequestURI(), nil))
	this.So(page.Code, should.Equal, http.StatusOK)
	this.So(page.Body.String(), should.ContainSubstring, "Vet this duck sighting")

	form := url.Values{"exp": {link.Query().Get("exp")}, "sig": {link.Query().Get("sig")}, "action": {"approve"}}
	req := httptest.NewRequest(http.MethodPost, link.Path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	this.So(this.do(req).Code, should.Equal, http.StatusOK)

	reg, err := this.store.Get(context.Background(), 1)
	this.So(err, should.BeNil)
	this.So(reg.Status, should.Equal, store.StatusApproved)

	forged := url.Values{"exp": {link.Query().Get("exp")}, "sig": {"bad"}, "action": {"delete"}}
	req = httptest.NewRequest(http.MethodPost, link.Path, strings.NewReader(forged.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	this.So(this.do(req).Code, should.Equal, http.StatusForbidden)
}

func (this *ServerFixture) TestRegistrationValidation() {
	rec := this.register("", "12 Candy Cane Ln")
	this.So(rec.Code, should.Equal, http.StatusBadRequest)
}

func (this *ServerFixture) TestRegistrationIsRateLimited() {
	for range 5 {
		this.So(this.register("Elf", "1 Pole Rd").Code, should.Equal, http.StatusCreated)
	}
	this.So(this.register("Elf", "1 Pole Rd").Code, should.Equal, http.StatusTooManyRequests)
}

func (this *ServerFixture) TestCORSOnlyForAllowedOrigins() {
	req := httptest.NewRequest(http.MethodOptions, "/api/public", nil)
	req.Header.Set("Origin", "https://smartybryan.github.io")
	this.So(this.do(req).Header().Get("Access-Control-Allow-Origin"), should.Equal, "https://smartybryan.github.io")

	req.Header.Set("Origin", "https://evil.example")
	this.So(this.do(req).Header().Get("Access-Control-Allow-Origin"), should.Equal, "")
}

func (this *ServerFixture) TestPublicLocation() {
	cases := map[[2]string]string{
		{"123 Maple St", "Springfield"}:        "Maple St, Springfield",
		{"123B Maple St Apt 4", "Springfield"}: "Maple St, Springfield",
		{"12-3 Oak Ave, Unit 7", "Town"}:       "Oak Ave, Town",
		{"Maple St", ""}:                       "Maple St",
		{"42", "Town"}:                         "42, Town",
	}
	for in, want := range cases {
		this.So(PublicLocation(in[0], in[1]), should.Equal, want)
	}
}

func (this *ServerFixture) putSettings(token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/owner/settings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return this.do(req)
}

func (this *ServerFixture) TestRegistrationEmailGoesToAddressFromSettings() {
	token := this.login()
	this.So(this.putSettings(token, `{"ownerName":"Santa","notifyEmail":"owner@example.com"}`).Code, should.Equal, http.StatusOK)

	this.register("The Elves", "12 Candy Cane Ln")
	this.So(this.mailer.waitForMail(), should.Equal, []string{"owner@example.com"})
}

func (this *ServerFixture) TestRegistrationEmailFallsBackWhenSettingsEmpty() {
	this.register("The Elves", "12 Candy Cane Ln")
	this.So(this.mailer.waitForMail(), should.Equal, []string{"fallback@example.com"})
}

func (this *ServerFixture) TestNotifyEmailIsNeverPublic() {
	token := this.login()
	this.putSettings(token, `{"ownerName":"Santa","notifyEmail":"owner@example.com"}`)

	rec := this.do(httptest.NewRequest(http.MethodGet, "/api/public", nil))
	this.So(rec.Body.String(), should.ContainSubstring, "Santa")
	this.So(rec.Body.String(), should.NotContainSubstring, "owner@example.com")
}

func (this *ServerFixture) TestOwnerSettingsIncludeEmailAndStatus() {
	token := this.login()
	this.putSettings(token, `{"notifyEmail":"owner@example.com"}`)

	req := httptest.NewRequest(http.MethodGet, "/api/owner/settings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	var got struct {
		NotifyEmail     string
		EmailConfigured bool
	}
	_ = json.Unmarshal(this.do(req).Body.Bytes(), &got)
	this.So(got.NotifyEmail, should.Equal, "owner@example.com")
	this.So(got.EmailConfigured, should.BeTrue)
}

func (this *ServerFixture) TestInvalidNotifyEmailRejected() {
	token := this.login()
	this.So(this.putSettings(token, `{"notifyEmail":"not an email"}`).Code, should.Equal, http.StatusBadRequest)
	this.So(this.putSettings(token, `{"notifyEmail":"Santa <s@example.com>"}`).Code, should.Equal, http.StatusBadRequest)
}

func (this *ServerFixture) TestSendTestEmail() {
	token := this.login()
	this.putSettings(token, `{"notifyEmail":"owner@example.com"}`)

	req := httptest.NewRequest(http.MethodPost, "/api/owner/test-email", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	this.So(this.do(req).Code, should.Equal, http.StatusOK)
	this.So(this.mailer.to, should.Equal, []string{"owner@example.com"})
}

func (this *ServerFixture) uploadHomePhoto(token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("photo", "home.png")
	_ = png.Encode(part, image.NewRGBA(image.Rect(0, 0, 30, 20)))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPut, "/api/owner/home-photo", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return this.do(req)
}

func (this *ServerFixture) homePhotoURL() string {
	rec := this.do(httptest.NewRequest(http.MethodGet, "/api/public", nil))
	var res struct{ HomePhotoURL string }
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	return res.HomePhotoURL
}

func (this *ServerFixture) TestHomePhotoUploadShowsOnTrail() {
	this.So(this.homePhotoURL(), should.Equal, "")

	token := this.login()
	this.So(this.uploadHomePhoto(token).Code, should.Equal, http.StatusOK)

	photoURL := this.homePhotoURL()
	this.So(photoURL, should.StartWith, "https://api.example.com/api/home-photo?v=")
	path := strings.TrimPrefix(photoURL, "https://api.example.com")
	rec := this.do(httptest.NewRequest(http.MethodGet, path, nil))
	this.So(rec.Code, should.Equal, http.StatusOK)
	this.So(rec.Header().Get("Content-Type"), should.Equal, "image/jpeg")
}

func (this *ServerFixture) TestHomePhotoRequiresOwner() {
	this.So(this.uploadHomePhoto("").Code, should.Equal, http.StatusUnauthorized)
	this.So(this.do(httptest.NewRequest(http.MethodDelete, "/api/owner/home-photo", nil)).Code, should.Equal, http.StatusUnauthorized)
}

func (this *ServerFixture) TestHomePhotoCanBeRemoved() {
	token := this.login()
	this.uploadHomePhoto(token)

	req := httptest.NewRequest(http.MethodDelete, "/api/owner/home-photo", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	this.So(this.do(req).Code, should.Equal, http.StatusOK)
	this.So(this.homePhotoURL(), should.Equal, "")
	this.So(this.do(httptest.NewRequest(http.MethodGet, "/api/home-photo", nil)).Code, should.Equal, http.StatusNotFound)
}

func (this *ServerFixture) TestHomePhotoURLChangesWhenReplaced() {
	token := this.login()
	this.uploadHomePhoto(token)
	first := this.homePhotoURL()
	this.now = this.now.Add(time.Second)
	this.uploadHomePhoto(token)
	this.So(this.homePhotoURL(), should.NotEqual, first)
}
