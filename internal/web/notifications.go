package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Luw17/go-content-go/internal/builder"
	"github.com/Luw17/go-content-go/internal/siteconfig"
)

var allowPrivateWebhookTargetsForTest = false

func (h *handler) exportFormSubmissionsCSV(w http.ResponseWriter, r *http.Request) {
	user, ok := h.requireContentAccess(w, r)
	if !ok {
		return
	}
	if user.Role == "author" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	submissions, err := h.builder.ListFormSubmissions(r.Context(), 5000)
	if err != nil {
		slog.Error("export form submissions CSV", "error", err)
		http.Error(w, "failed to export submissions", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="form-submissions.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"id", "form_id", "page_path", "sender_name", "sender_email", "message", "created_at"})
	for _, sub := range submissions {
		_ = writer.Write([]string{
			strconv.FormatInt(sub.ID, 10),
			sanitizeCSVCell(sub.FormID),
			sanitizeCSVCell(sub.PagePath),
			sanitizeCSVCell(sub.SenderName),
			sanitizeCSVCell(sub.SenderEmail),
			sanitizeCSVCell(sub.Message),
			sanitizeCSVCell(sub.CreatedAt),
		})
	}
	writer.Flush()
}

func sanitizeCSVCell(value string) string {
	if value == "" {
		return ""
	}
	trimmed := strings.TrimLeft(value, " ")
	if trimmed != "" {
		switch trimmed[0] {
		case '=', '+', '-', '@', '\t', '\r':
			return "'" + value
		}
	}
	return value
}

func sanitizeEmailHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\x00", "")
	return strings.TrimSpace(value)
}

func (h *handler) dispatchFormNotifications(sub builder.FormSubmission) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	settings, err := siteconfig.Load(ctx, h.db)
	if err != nil {
		return
	}
	if settings.FormWebhookURL != "" {
		if err := sendFormWebhook(ctx, settings.FormWebhookURL, sub); err != nil {
			slog.Warn("form webhook delivery failed", "url", settings.FormWebhookURL, "error", err)
		}
	}
	if settings.SMTPHost != "" && settings.FormNotifyEmail != "" {
		if err := sendFormSMTP(ctx, settings, sub); err != nil {
			slog.Warn("form SMTP notification failed", "host", settings.SMTPHost, "error", err)
		}
	}
}

func sendFormWebhook(ctx context.Context, rawURL string, sub builder.FormSubmission) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	payload, err := json.Marshal(map[string]any{
		"event":        "form.submitted",
		"form_id":      sub.FormID,
		"page_path":    sub.PagePath,
		"sender_name":  sub.SenderName,
		"sender_email": sub.SenderEmail,
		"message":      sub.Message,
		"submitted_at": time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := safeWebhookHTTPClient(allowPrivateWebhookTargetsForTest)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func safeWebhookHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("webhook host resolved to no IP addresses")
			}
			for _, ip := range ips {
				if !allowPrivate && isBlockedWebhookIP(ip) {
					return nil, fmt.Errorf("webhook target IP %s is blocked", ip.String())
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many webhook redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("unsupported webhook redirect scheme")
			}
			return nil
		},
	}
}

func isBlockedWebhookIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func sendFormSMTP(ctx context.Context, settings siteconfig.Settings, sub builder.FormSubmission) error {
	host := sanitizeEmailHeader(settings.SMTPHost)
	port := sanitizeEmailHeader(settings.SMTPPort)
	if port == "" {
		port = "587"
	}
	from := sanitizeEmailHeader(settings.SMTPFrom)
	if from == "" {
		from = sanitizeEmailHeader(settings.SMTPUser)
	}
	if from == "" {
		from = "noreply@localhost"
	}
	to := sanitizeEmailHeader(settings.FormNotifyEmail)
	if host == "" || to == "" {
		return errors.New("SMTP host and recipient are required")
	}
	subject := sanitizeEmailHeader("Novo envio no formulário " + sub.FormID)
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nPágina: %s\nNome: %s\nE-mail: %s\n\nMensagem:\n%s\n",
		from, to, subject, sanitizeEmailHeader(sub.PagePath), sanitizeEmailHeader(sub.SenderName), sanitizeEmailHeader(sub.SenderEmail), sub.Message)

	addr := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(6 * time.Second)
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if settings.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", settings.SMTPUser, settings.SMTPPass, host)); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
