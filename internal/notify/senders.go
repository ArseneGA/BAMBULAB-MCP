package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// permanentError : inutile de réessayer (configuration ou refus définitif).
type permanentError struct{ err error }

func (p *permanentError) Error() string { return p.err.Error() }
func (p *permanentError) Unwrap() error { return p.err }

func permanent(f string, a ...any) error { return &permanentError{fmt.Errorf(f, a...)} }

var urlInText = regexp.MustCompile(`https?://\S+`)

// scrubErr retire des messages d'erreur toute URL (l'URL d'un sujet ntfy ou d'un webhook est un secret).
func scrubErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return urlInText.ReplaceAllString(err.Error(), "<url>")
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return invalidf("URL invalide : http(s)://hôte/chemin attendu, sans identifiants intégrés")
	}
	return nil
}

func (s *Service) mustSecret(ctx context.Context, ch ChannelView, field string) (string, error) {
	v, _, err := s.secret(ctx, ch, field)
	if err != nil {
		return "", permanent("secret %s illisible", field)
	}
	return v, nil
}

// hdr rend une valeur sûre pour un en-tête HTTP (une ligne ; UTF-8 encodé RFC 2047 que ntfy décode).
func hdr(v string) string {
	v = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, v)
	for _, r := range v {
		if r > 126 || r < 32 {
			return mime.BEncoding.Encode("UTF-8", v)
		}
	}
	return v
}

func (s *Service) do(req *http.Request) error {
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return errors.New(scrubErr(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 == 2 {
		return nil
	}
	if resp.StatusCode/100 == 4 && resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode != http.StatusRequestTimeout {
		return permanent("le serveur a répondu %d", resp.StatusCode)
	}
	return fmt.Errorf("le serveur a répondu %d", resp.StatusCode)
}

// ---- ntfy ----

func (s *Service) sendNtfy(ctx context.Context, ch ChannelView, e Event) error {
	u, err := s.mustSecret(ctx, ch, "url")
	if err != nil {
		return err
	}
	if err := checkURL(u); err != nil {
		return permanent("URL du sujet invalide")
	}
	tok, _ := s.mustSecret(ctx, ch, "token")
	var req *http.Request
	if len(e.Photo) > 0 && len(e.Photo) <= 2<<20 {
		req, err = http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(e.Photo)) // pièce jointe locale ntfy
		if err == nil {
			req.Header.Set("Filename", "photo.jpg")
			req.Header.Set("Message", hdr(e.Message))
			req.Header.Set("Content-Type", "image/jpeg")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(e.Message))
		if err == nil {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	if err != nil {
		return permanent("requête ntfy invalide")
	}
	req.Header.Set("Title", hdr(e.Title))
	if e.Link != "" {
		req.Header.Set("Click", hdr(e.Link))
	}
	if p := ch.Config.Priority; p != "" {
		req.Header.Set("Priority", p)
	} else if e.Kind == EvDetectionAlert || e.Kind == EvPrintFailed || e.Kind == EvStartUnknown {
		req.Header.Set("Priority", "high")
	}
	req.Header.Set("Tags", hdr(ntfyTag(e.Kind)))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return s.do(req)
}

func ntfyTag(kind string) string {
	switch kind {
	case EvPrintCompleted:
		return "white_check_mark"
	case EvPrintFailed, EvDetectionAlert:
		return "warning"
	case EvStartUnknown:
		return "question"
	case EvPrinterOffline:
		return "electric_plug"
	}
	return "printer"
}

// ---- webhook générique ----

// WebhookPayload : corps JSON d'un webhook. Aucun secret.
type WebhookPayload struct {
	Event     string `json:"event"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Link      string `json:"link,omitempty"`
	PrinterID string `json:"printer_id,omitempty"`
	JobID     string `json:"print_job_id,omitempty"`
	PhotoURL  string `json:"photo_url,omitempty"`
	At        string `json:"at"`
}

// Sign calcule la signature d'un webhook : HMAC-SHA256(clé, horodatage + "." + corps), en hexadécimal.
func Sign(key, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(timestamp))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Service) sendWebhook(ctx context.Context, ch ChannelView, e Event) error {
	u, err := s.mustSecret(ctx, ch, "url")
	if err != nil {
		return err
	}
	if err := checkURL(u); err != nil {
		return permanent("URL du webhook invalide")
	}
	key, err := s.mustSecret(ctx, ch, "hmac_key")
	if err != nil {
		return err
	}
	if key == "" {
		return permanent("clé HMAC absente : un webhook non signé n'est pas envoyé")
	}
	body, _ := json.Marshal(WebhookPayload{Event: e.Kind, Title: e.Title, Message: e.Message, Link: e.Link, PrinterID: e.PrinterID,
		JobID: e.JobID, PhotoURL: e.PhotoURL, At: e.At.UTC().Format(time.RFC3339)})
	ts := strconv.FormatInt(s.now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return permanent("requête webhook invalide")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "bambu-mcp-notify/1")
	req.Header.Set("X-Bambu-Timestamp", ts)
	req.Header.Set("X-Bambu-Signature", "sha256="+Sign(key, ts, body))
	return s.do(req)
}

// ---- e-mail SMTP (TLS obligatoire) ----

func (s *Service) sendEmail(ctx context.Context, ch ChannelView, e Event) error {
	c := ch.Config
	pass, _ := s.mustSecret(ctx, ch, "password")
	from, err := mail.ParseAddress(c.From)
	if err != nil {
		return permanent("adresse d'expéditeur invalide")
	}
	var rcpts []string
	for _, t := range c.To {
		a, err := mail.ParseAddress(t)
		if err != nil {
			return permanent("destinataire invalide")
		}
		rcpts = append(rcpts, a.Address)
	}
	msg, err := buildMail(from, c.To, e, s.now())
	if err != nil {
		return permanent("message e-mail invalide")
	}
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	tcfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.d.SMTPTLS != nil {
		tcfg = s.d.SMTPTLS.Clone()
	}
	tcfg.ServerName = c.SMTPHost
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if c.SMTPTLS == "implicit" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tcfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connexion SMTP : %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	cl, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		return fmt.Errorf("dialogue SMTP : %w", err)
	}
	defer cl.Close()
	if c.SMTPTLS != "implicit" {
		if ok, _ := cl.Extension("STARTTLS"); !ok {
			return permanent("le serveur n'offre pas STARTTLS : le SMTP en clair est refusé")
		}
		if err := cl.StartTLS(tcfg); err != nil {
			return permanent("STARTTLS refusé ou certificat non vérifié : %v", err)
		}
	}
	if c.SMTPUser != "" && pass != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.SMTPUser, pass, c.SMTPHost)); err != nil {
			return permanent("authentification SMTP refusée")
		}
	}
	if err := cl.Mail(from.Address); err != nil {
		return fmt.Errorf("SMTP MAIL : %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("SMTP RCPT : %w", err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA : %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("SMTP écriture : %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP fin de message : %w", err)
	}
	return cl.Quit()
}

func buildMail(from *mail.Address, to []string, e Event, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	subj := strings.NewReplacer("\r", " ", "\n", " ").Replace(e.Title)
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", from.String())
	h("To", strings.Join(to, ", "))
	h("Subject", mime.QEncoding.Encode("UTF-8", subj))
	h("Date", now.Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	mw := multipart.NewWriter(&b)
	h("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
	b.WriteString("\r\n")
	body := e.Message
	if e.Link != "" {
		body += "\r\n\r\n" + e.Link
	}
	ph, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
	qp := quotedprintable.NewWriter(ph)
	_, _ = qp.Write([]byte(body))
	_ = qp.Close()
	if len(e.Photo) > 0 && len(e.Photo) <= 2<<20 {
		ah, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"image/jpeg"}, "Content-Transfer-Encoding": {"base64"},
			"Content-Disposition": {`attachment; filename="photo.jpg"`}})
		enc := base64.StdEncoding.EncodeToString(e.Photo)
		for len(enc) > 76 {
			_, _ = io.WriteString(ah, enc[:76]+"\r\n")
			enc = enc[76:]
		}
		_, _ = io.WriteString(ah, enc+"\r\n")
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
