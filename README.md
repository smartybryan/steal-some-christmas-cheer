# 🦆 Steal Some Christmas Cheer

A neighborhood holiday game. An inflatable duck in a Santa hat and scarf sits on a lawn with a sign:
**"Steal Me For Some Christmas Cheer."** Families plan a heist, steal the duck, display it in their own
yard, and scan the QR code on the sign to register.

The website shows the duck's trail, but **never the current location**. A stop appears only after
someone else steals the duck from that house, so finding it is part of the fun.

## How it fits together

```
GitHub Pages (docs/)  ──fetch──▶  Go server (you host it)  ──▶  SQLite (data/duck.db)
 index.html  trail                 /api/public  /api/register        registrations + photos
 register.html  QR target          /api/owner/*  (password login)     owner settings
 owner.html  vetting + settings    /review/{id}  (signed link from email/text)
 sign.html  printable lawn sign    ──▶ email (SMTP) and/or text (Twilio) to the owner
```

- **Secret location:** the server leaves out the newest registration (pending or approved) from every public
  response, including its photo. Turn on "Reveal the final location" in owner settings after the season ends.
- **Vetting:** every registration starts as pending. You get an email or text with a signed link to approve,
  hide the photo, or delete the entry, right from your phone. You can do the same on `owner.html`.
- **Privacy:** only the street name and city are shown publicly (e.g. "Maple St, Springfield"). You can
  edit that text when you approve. Photos are re-encoded server-side, which strips EXIF/GPS metadata.
- **Abuse protection:** registrations are rate-limited per IP (5/hour), with a honeypot field and upload size limits.

## Running the server

```bash
OWNER_PASSWORD='pick-something-good' SECRET="$(openssl rand -hex 32)" go run .
```

Then open http://localhost:8080. The Go server also serves the website, so GitHub Pages is optional.

| Variable | Required | Description |
|---|---|---|
| `OWNER_PASSWORD` | yes | Password for `owner.html`. |
| `SECRET` | strongly recommended | Random string used to sign logins and review links. If unset, a random one is generated and links break on restart. |
| `PORT` | | Default `8080`. |
| `DATA_DIR` | | Where `duck.db` lives. Default `./data`. **Back this up.** |
| `PUBLIC_URL` | yes in production | External URL of this server, e.g. `https://duck.example.com`. Used in photo URLs and review links. |
| `SITE_URL` | | URL of the website, e.g. `https://smartybryan.github.io/steal-some-christmas-cheer`. Defaults to `PUBLIC_URL`. |
| `ALLOWED_ORIGINS` | if using GitHub Pages | Comma-separated origins allowed to call the API, e.g. `https://smartybryan.github.io`. |
| `TRUST_PROXY` | | `true` if behind a reverse proxy, so rate limiting uses `X-Forwarded-For`. |
| `NOTIFY_EMAIL` | | Email address(es) to notify, comma-separated. Enables email. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | with email | SMTP over STARTTLS (port 587). Works with Gmail app passwords, Resend, SendGrid, etc. |
| `NOTIFY_SMS` | | Phone number to text, e.g. `+18015551234`. Enables SMS. |
| `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM` | with SMS | Twilio credentials and sending number. |

Notifications are always written to the server log as well.

### Docker

```bash
docker build -t duck .
docker run -p 8080:8080 -v duck-data:/data \
  -e OWNER_PASSWORD=... -e SECRET=... -e PUBLIC_URL=https://duck.example.com duck
```

The server needs HTTPS in production, since phones will upload photos and you'll log in. Put it behind
Caddy, nginx, Cloudflare Tunnel, or a host that terminates TLS (and set `TRUST_PROXY=true`).

## Hosting the website on GitHub Pages (optional)

1. Edit `docs/config.js` and set `apiBase` to your server's `PUBLIC_URL`.
2. On the server, set `ALLOWED_ORIGINS=https://smartybryan.github.io` and
   `SITE_URL=https://smartybryan.github.io/steal-some-christmas-cheer`.
3. In the repo on GitHub, go to **Settings → Pages**, choose **Deploy from a branch**, and pick `main` / `/docs`.

## Setting up for the season

1. Log in at `owner.html` and fill in **Settings**: your name, Duck HQ street, return address, return-by date
   and any instructions.
2. Open `sign.html` and print it. The QR code points to `register.html` on whatever site you opened it from,
   so open it from the real public URL before printing.
3. Laminate the sign, put the duck out, and wait for the first heist. 🎄
4. After New Year's, turn on **Reveal the final location** to show the whole trail.

To start a new season, stop the server and move `data/duck.db` aside (your settings live there too).

## Development

```bash
go build ./...
go test ./...
```

Tests use [gunit](https://github.com/smarty/gunit).
