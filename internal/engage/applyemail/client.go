// Package applyemail sends a candidate's own job application to the candidate's own
// inbox instead of the employer -- a personal review-before-you-forward-it-yourself
// flow. Deliberately independent of internal/engage/emailnotify (the AWS SES sender
// this app otherwise uses for account mail): Resend needs one API key over plain
// HTTPS, no AWS account, no IAM policy, no per-region verified sender identity.
package applyemail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Attachment is one file to attach. Content is raw bytes; Send base64-encodes it,
// which is the form Resend's JSON API wants.
type Attachment struct {
	Filename string
	Content  []byte
}

// Client sends mail through Resend's HTTP API
// (https://resend.com/docs/api-reference/emails/send-email).
type Client struct {
	apiKey     string
	from       string
	httpClient *http.Client
}

// NewClient builds a Client. from should be an address on a domain verified in your
// Resend account, or "onboarding@resend.dev" -- Resend's shared sender, which needs
// no domain verification and is fine for personal use.
func NewClient(apiKey, from string) *Client {
	return &Client{apiKey: apiKey, from: from, httpClient: &http.Client{}}
}

// Configured reports whether enough is set to attempt a send. Callers use this to
// degrade to a clear "not set up" response rather than a confusing send failure.
func (c *Client) Configured() bool {
	return c != nil && c.apiKey != "" && c.from != ""
}

type sendRequest struct {
	From        string           `json:"from"`
	To          []string         `json:"to"`
	Subject     string           `json:"subject"`
	HTML        string           `json:"html"`
	Attachments []sendAttachment `json:"attachments,omitempty"`
}

type sendAttachment struct {
	Filename string `json:"filename"`
	// Content is base64, per Resend's API -- there is no multipart path.
	Content string `json:"content"`
}

// Send POSTs one email to every address in to. Every recipient is meant to be the
// account holder's own -- this package has no notion of a third-party recipient, by
// design: the whole point of applyemail is that nothing here ever reaches an employer.
func (c *Client) Send(ctx context.Context, to []string, subject, html string, attachments []Attachment) error {
	if !c.Configured() {
		return fmt.Errorf("applyemail: not configured (RESEND_API_KEY/RESEND_FROM_EMAIL unset)")
	}
	if len(to) == 0 {
		return fmt.Errorf("applyemail: no recipients configured (APPLY_FORWARD_EMAILS unset)")
	}

	req := sendRequest{From: c.from, To: to, Subject: subject, HTML: html}
	for _, a := range attachments {
		req.Attachments = append(req.Attachments, sendAttachment{
			Filename: a.Filename,
			Content:  base64.StdEncoding.EncodeToString(a.Content),
		})
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("applyemail: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("applyemail: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("applyemail: send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("applyemail: resend returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
