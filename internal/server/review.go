package server

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/smartybryan/steal-some-christmas-cheer/internal/store"
)

// The review pages are reached from the link in the owner's email or text,
// so the owner can vet a photo from their phone without logging in. The link
// is HMAC-signed and expires. Viewing the page changes nothing; only the form
// POST does, so link-preview bots that fetch the URL can't approve anything.

var reviewTemplate = template.Must(template.New("review").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vet a Duck Photo</title>
<style>
 body{font-family:system-ui,sans-serif;background:#fdf6ec;color:#2b2118;margin:0;padding:16px}
 main{max-width:560px;margin:0 auto;background:#fff;border-radius:16px;padding:20px;box-shadow:0 4px 18px rgba(0,0,0,.08)}
 h1{color:#b3202a;margin-top:0;font-size:1.4rem}
 img{width:100%;border-radius:12px;margin:12px 0}
 dt{font-weight:700;margin-top:8px} dd{margin:0}
 label{display:block;font-weight:700;margin-top:12px}
 input{width:100%;box-sizing:border-box;padding:10px;font-size:1rem;border:1px solid #ccc;border-radius:8px}
 .buttons{display:flex;flex-wrap:wrap;gap:8px;margin-top:16px}
 button{flex:1;padding:12px;font-size:1rem;border:0;border-radius:10px;color:#fff;cursor:pointer;min-width:140px}
 .approve{background:#1f7a4d}.reject{background:#c77700}.delete{background:#8a1c24}
 .status{display:inline-block;padding:2px 10px;border-radius:99px;background:#eee;font-size:.9rem}
 .done{text-align:center;font-size:1.2rem}
</style></head><body><main>
{{if .Done}}
 <h1>{{.Done}}</h1>
 <p class="done"><a href="{{.SiteURL}}/owner.html">Open the owner page</a></p>
{{else}}
 <h1>🦆 Vet this duck sighting</h1>
 <span class="status">Status: {{.Reg.Status}}</span>
 {{if .Reg.HasPhoto}}<img src="{{.PhotoURL}}" alt="Photo uploaded by {{.Reg.Name}}">{{end}}
 <dl>
  <dt>Full address (private)</dt><dd>{{.Reg.Street}}, {{.Reg.City}}</dd>
  {{if .Reg.Message}}<dt>Message</dt><dd>{{.Reg.Message}}</dd>{{end}}
 </dl>
 <form method="post">
  <input type="hidden" name="exp" value="{{.Exp}}">
  <input type="hidden" name="sig" value="{{.Sig}}">
  <label for="name">Name shown publicly</label>
  <input id="name" name="name" value="{{.Reg.Name}}" maxlength="60">
  <label for="loc">Location shown publicly</label>
  <input id="loc" name="publicLocation" value="{{.Reg.PublicLocation}}" maxlength="120">
  <div class="buttons">
   <button class="approve" name="action" value="approve">✅ Approve</button>
   <button class="reject" name="action" value="reject_photo">🙈 Hide photo, keep entry</button>
   <button class="delete" name="action" value="delete" onclick="return confirm('Delete this registration permanently?')">🗑️ Delete (spam)</button>
  </div>
 </form>
{{end}}
</main></body></html>`))

type reviewView struct {
	Reg      store.Registration
	PhotoURL string
	Exp, Sig string
	SiteURL  string
	Done     string
}

func (s *Server) verifyReviewLink(w http.ResponseWriter, r *http.Request, exp, sig string) (int64, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return 0, false
	}
	expires, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || !s.cfg.Signer.Verify(reviewPurpose(id), expires, sig, s.cfg.Now()) {
		http.Error(w, "This review link is invalid or has expired. Use the owner page instead.", http.StatusForbidden)
		return 0, false
	}
	return id, true
}

func (s *Server) handleReviewPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, ok := s.verifyReviewLink(w, r, q.Get("exp"), q.Get("sig"))
	if !ok {
		return
	}
	reg, err := s.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.renderReview(w, reviewView{Done: "That registration was already deleted."})
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.renderReview(w, reviewView{
		Reg:      reg,
		PhotoURL: r.URL.Path + "/photo?" + q.Encode(),
		Exp:      q.Get("exp"),
		Sig:      q.Get("sig"),
	})
}

func (s *Server) handleReviewPhoto(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, ok := s.verifyReviewLink(w, r, q.Get("exp"), q.Get("sig"))
	if !ok {
		return
	}
	photo, err := s.store.Photo(r.Context(), id)
	writePhoto(w, photo, err, "private, max-age=300")
}

func (s *Server) handleReviewSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id, ok := s.verifyReviewLink(w, r, r.PostFormValue("exp"), r.PostFormValue("sig"))
	if !ok {
		return
	}
	req := reviewRequest{
		Action:         r.PostFormValue("action"),
		Name:           r.PostFormValue("name"),
		PublicLocation: r.PostFormValue("publicLocation"),
	}
	if err := s.applyReview(r.Context(), id, req); err != nil && !errors.Is(err, store.ErrNotFound) {
		reviewError(w, err)
		return
	}
	done := map[string]string{
		"approve":      "✅ Approved! It will appear on the trail once the duck moves on.",
		"reject_photo": "🙈 Photo hidden. The stop will still appear on the trail.",
		"delete":       "🗑️ Deleted.",
	}[req.Action]
	s.renderReview(w, reviewView{Done: done})
}

func (s *Server) renderReview(w http.ResponseWriter, v reviewView) {
	v.SiteURL = s.cfg.SiteURL
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err := reviewTemplate.Execute(w, v); err != nil {
		serverError(w, err)
	}
}
