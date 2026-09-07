package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/strelov1/freehire/internal/application/jobtracking"
	"github.com/strelov1/freehire/internal/candidate/cv"
	"github.com/strelov1/freehire/internal/engage/applyemail"
	"github.com/strelov1/freehire/internal/engage/telegramnotify"
	"github.com/strelov1/freehire/internal/ingest/applyform"
	"github.com/strelov1/freehire/internal/platform/db"
)

// NotifyApplied implements jobtracking.ApplyNotifier: it is called every time this
// deployment records an application, from whichever caller recorded it -- the Apply
// button's own application form on the job page, the in-app assistant's apply tool,
// or the mail-reconstruction path that notices a confirmation email. Personal-use
// feature, not part of the upstream product: instead of the application going
// anywhere near the employer, it packages what a real application would carry and
// emails that packet to the addresses in APPLY_FORWARD_EMAILS, for the account
// holder to review and forward by hand if they choose to. This method never
// contacts the employer.
//
// `submission` is the candidate's own filled-in form when the caller collected one
// (today, only the HTTP apply door does -- see JobView.svelte's apply dialog and
// trackingHandlers.MarkApplied) and nil otherwise (the assistant tool, mail
// reconstruction). When present, it is the primary content of the email: the name/
// email/phone and screening answers the account holder actually typed, rather than
// this method's own best-effort reconstruction from stored profile data. The
// reconstruction (drafted cover letter, tailored CV, the employer's own published
// questions) still runs every time, either as the whole email (no submission) or as
// supporting context alongside the submission.
//
// Silently does nothing (nil error) when RESEND_API_KEY/RESEND_FROM_EMAIL/
// APPLY_FORWARD_EMAILS aren't set -- an unconfigured deployment behaves exactly
// like one where this feature doesn't exist, the same stance RenderCVPDF takes on
// an absent typst binary. Every other piece is best-effort: a job with no captured
// form, no drafted letter, or no tailored CV still sends -- those sections simply
// don't appear.
//
// Wired in from the composition root (see handler.go) via
// trackingH.tracking.SetApplyNotifier(cvH) -- jobtracking.Service calls this on
// its own goroutine-free, synchronous path, and itself only logs a failure rather
// than surfacing it, so a slow or failing send never turns a successful "mark
// applied" into an error response.
func (h *cvHandlers) NotifyApplied(ctx context.Context, userID, jobID int64, slug string, submission *jobtracking.ApplySubmission) error {
	apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	from := strings.TrimSpace(os.Getenv("RESEND_FROM_EMAIL"))
	to := splitAndTrim(os.Getenv("APPLY_FORWARD_EMAILS"))
	emailConfigured := apiKey != "" && from != "" && len(to) > 0

	// Same personal-use stance as the email above, on a second channel: a bot token
	// this deployment already has (TELEGRAM_BOT_TOKEN, shared with the saved-search
	// notifier) plus a fixed list of chat IDs to post applications to. Independent of
	// the per-user /me/telegram link table on purpose -- this goes to the account
	// holder's own chat(s) regardless of which user's session recorded the
	// application, exactly like APPLY_FORWARD_EMAILS is a fixed address list rather
	// than "the applying user's own email".
	botToken := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	chatIDs := parseChatIDs(os.Getenv("APPLY_TELEGRAM_CHAT_IDS"))
	telegramConfigured := botToken != "" && len(chatIDs) > 0

	if !emailConfigured && !telegramConfigured {
		return nil
	}

	job, err := h.jobReader.GetJob(ctx, jobID)
	if err != nil {
		return fmt.Errorf("apply-email: load job %d: %w", jobID, err)
	}

	// The employer's own screening questions, when their ATS publishes a readable form.
	// Absent for most postings -- that is the ordinary case, not an error (see JobApplyForm).
	var form *applyform.Display
	if row, err := h.queries.GetApplyFormByJobID(ctx, jobID); err == nil {
		var f applyform.Form
		if err := json.Unmarshal(row.Payload, &f); err == nil {
			f.Provider = row.Provider
			d := f.ForDisplay()
			form = &d
		}
	}

	// A cover letter the account holder already drafted for this job through the CV
	// builder's own cover-letter feature -- read-only here, nothing is drafted on this
	// path (drafting spends a model call and a plan allowance; recording an application
	// should not).
	var letterBody string
	if h.letter.letters != nil {
		if stored, err := h.letter.letters.Get(ctx, userID, jobID); err == nil && stored != nil {
			letterBody = stored.Body
		}
	}

	// The tailored copy of the CV bound to this job, if the account holder started
	// tailoring for it (ListTailored's JobSlug is exactly that binding). Renders through
	// the same path as the CV builder's own "Download PDF" button (RenderCVPDF above).
	var attachments []applyemail.Attachment
	if emailConfigured && h.cvRenderer != nil {
		if items, err := h.cvStore.ListTailored(ctx, userID); err == nil {
			for _, it := range items {
				if it.JobSlug != slug {
					continue
				}
				rec, err := h.cvStore.Get(ctx, it.ID, userID)
				if err != nil {
					break
				}
				tmpl, err := cv.ResolveTemplate(rec.TemplateID)
				if err != nil {
					break
				}
				pdf, err := h.cvRenderer.Render(ctx, rec.Document, tmpl,
					headshotForTemplate(ctx, h.photos, userID, tmpl),
					h.tracedHrefs(ctx, rec, userID))
				if err == nil {
					attachments = append(attachments, applyemail.Attachment{Filename: "cv.pdf", Content: pdf})
				}
				break
			}
		}
	}

	var errs []error

	if emailConfigured {
		subject := fmt.Sprintf("Application recorded: %s at %s", job.Title, job.Company)
		client := applyemail.NewClient(apiKey, from)
		body := applicationEmailHTML(job, form, letterBody, len(attachments) > 0, submission)
		if err := client.Send(ctx, to, subject, body, attachments); err != nil {
			errs = append(errs, fmt.Errorf("apply-email: %w", err))
		}
	}

	if telegramConfigured {
		msg := applicationTelegramMessage(job, submission, emailConfigured, len(attachments) > 0)
		bot := telegramnotify.NewClient(botToken)
		for _, chatID := range chatIDs {
			if err := bot.SendMessage(ctx, chatID, msg); err != nil {
				errs = append(errs, fmt.Errorf("apply-telegram: chat %d: %w", chatID, err))
			}
		}
	}

	return errors.Join(errs...)
}

// splitAndTrim reads a comma-separated env value into its non-empty, trimmed parts.
func splitAndTrim(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseChatIDs reads APPLY_TELEGRAM_CHAT_IDS -- a comma-separated list of Telegram
// chat IDs (get your own from @userinfobot after messaging the bot, or read it back
// off GET /me/telegram once you've linked your account through the site) -- into
// int64s. An entry that fails to parse is logged and skipped rather than failing
// the whole notification over one typo; the remaining valid chat IDs still get
// posted to.
func parseChatIDs(raw string) []int64 {
	var out []int64
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			log.Printf("apply-telegram: skipping invalid APPLY_TELEGRAM_CHAT_IDS entry %q: %v", part, err)
			continue
		}
		out = append(out, id)
	}
	return out
}

// applicationTelegramMessage renders the same "application recorded" packet as
// applicationEmailHTML, but for Telegram: parse_mode HTML only understands a small
// tag subset (b, i, a, code, pre, u, s, tg-spoiler -- no headings, paragraphs, or
// divs -- see telegramnotify.Client.SendMessage), so this is plain lines with
// bold/link emphasis rather than sharing the email's markup. It is a quick
// heads-up, not a replacement: the full packet (cover letter, the employer's own
// screening questions, the CV itself) still only ever goes out by email, when that
// channel is configured.
func applicationTelegramMessage(job db.Job, submission *jobtracking.ApplySubmission, emailConfigured, cvAttached bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\U0001F4E9 <b>Application recorded</b>\n%s\n%s\n",
		html.EscapeString(job.Title), html.EscapeString(job.Company))
	if job.URL != "" {
		fmt.Fprintf(&b, "<a href=\"%s\">Real posting</a>\n", html.EscapeString(job.URL))
	}

	if submission != nil {
		var contact []string
		if name := submission.FullName(); name != "" {
			contact = append(contact, html.EscapeString(name))
		}
		if submission.Email != "" {
			contact = append(contact, html.EscapeString(submission.Email))
		}
		if submission.Phone != "" {
			contact = append(contact, html.EscapeString(submission.Phone))
		}
		if len(contact) > 0 {
			fmt.Fprintf(&b, "\n%s\n", strings.Join(contact, " \u00b7 "))
		}
	}

	if emailConfigured {
		if cvAttached {
			b.WriteString("\n\U0001F4CE Tailored CV attached in the email.\n")
		}
		b.WriteString("\nFull details sent to your email.")
	}
	return b.String()
}

// writeSubmissionSections renders the candidate's own filled-in application --
// personal & contact, work history, education, skills, legal & eligibility
// disclosures, then the free-form message and the employer's own screening
// questions as answered -- in the same order the Apply form itself uses. Every
// section is skipped entirely when the candidate left it blank, rather than
// printing an empty heading.
func writeSubmissionSections(b *strings.Builder, submission *jobtracking.ApplySubmission) {
	var contact []string
	if name := submission.FullName(); name != "" {
		contact = append(contact, html.EscapeString(name))
	}
	if submission.Email != "" {
		contact = append(contact, html.EscapeString(submission.Email))
	}
	if submission.Phone != "" {
		contact = append(contact, html.EscapeString(submission.Phone))
	}
	if loc := submission.Location(); loc != "" {
		contact = append(contact, html.EscapeString(loc))
	}
	if len(contact) > 0 {
		fmt.Fprintf(b, "<h3>Applicant</h3><p>%s</p>", strings.Join(contact, " &middot; "))
	}

	if len(submission.WorkHistory) > 0 {
		b.WriteString("<h3>Work history (as submitted)</h3>")
		for _, w := range submission.WorkHistory {
			b.WriteString("<div>")
			var head []string
			if w.Title != "" {
				head = append(head, html.EscapeString(w.Title))
			}
			if w.Company != "" {
				head = append(head, html.EscapeString(w.Company))
			}
			if len(head) > 0 {
				fmt.Fprintf(b, "<p><strong>%s</strong></p>", strings.Join(head, " &middot; "))
			}
			if w.StartDate != "" || w.EndDate != "" {
				end := w.EndDate
				if end == "" {
					end = "Present"
				}
				fmt.Fprintf(b, "<p>%s &ndash; %s</p>", html.EscapeString(w.StartDate), html.EscapeString(end))
			}
			if w.Responsibilities != "" {
				fmt.Fprintf(b, "<p>%s</p>", html.EscapeString(w.Responsibilities))
			}
			if w.ReasonForLeaving != "" {
				fmt.Fprintf(b, "<p><em>Reason for leaving: %s</em></p>", html.EscapeString(w.ReasonForLeaving))
			}
			b.WriteString("</div>")
		}
	}

	if len(submission.Education) > 0 || submission.Certifications != "" {
		b.WriteString("<h3>Education &amp; certifications (as submitted)</h3>")
		for _, e := range submission.Education {
			var line []string
			for _, p := range []string{e.Degree, e.FieldOfStudy, e.School} {
				if p != "" {
					line = append(line, html.EscapeString(p))
				}
			}
			if e.GraduationYear != "" {
				line = append(line, html.EscapeString(e.GraduationYear))
			}
			if len(line) > 0 {
				fmt.Fprintf(b, "<p>%s</p>", strings.Join(line, " &middot; "))
			}
		}
		if submission.Certifications != "" {
			fmt.Fprintf(b, "<p><strong>Licenses &amp; certifications:</strong> %s</p>", html.EscapeString(submission.Certifications))
		}
	}

	if submission.HardSkills != "" || submission.Languages != "" || submission.SoftSkills != "" {
		b.WriteString("<h3>Skills &amp; qualifications (as submitted)</h3>")
		if submission.HardSkills != "" {
			fmt.Fprintf(b, "<p><strong>Skills:</strong> %s</p>", html.EscapeString(submission.HardSkills))
		}
		if submission.Languages != "" {
			fmt.Fprintf(b, "<p><strong>Languages:</strong> %s</p>", html.EscapeString(submission.Languages))
		}
		if submission.SoftSkills != "" {
			fmt.Fprintf(b, "<p><strong>Soft skills:</strong> %s</p>", html.EscapeString(submission.SoftSkills))
		}
	}

	if submission.WorkAuthorized != "" || submission.NeedsSponsorship != "" || submission.EEORace != "" ||
		submission.EEOGender != "" || submission.EEOVeteran != "" || submission.EEODisability != "" {
		b.WriteString("<h3>Legal &amp; eligibility (as submitted)</h3>")
		if submission.WorkAuthorized != "" {
			fmt.Fprintf(b, "<p><strong>Authorized to work:</strong> %s</p>", html.EscapeString(submission.WorkAuthorized))
		}
		if submission.NeedsSponsorship != "" {
			fmt.Fprintf(b, "<p><strong>Needs visa sponsorship:</strong> %s</p>", html.EscapeString(submission.NeedsSponsorship))
		}
		if submission.EEORace != "" || submission.EEOGender != "" || submission.EEOVeteran != "" || submission.EEODisability != "" {
			b.WriteString("<p><em>Voluntary self-identification, as answered:</em></p><ul>")
			// A fixed slice, not a map: map iteration order is randomized in Go, and this
			// list would otherwise reorder itself on every single email.
			for _, row := range []struct{ label, val string }{
				{"Race/ethnicity", submission.EEORace},
				{"Gender", submission.EEOGender},
				{"Veteran status", submission.EEOVeteran},
				{"Disability", submission.EEODisability},
			} {
				if row.val != "" {
					fmt.Fprintf(b, "<li>%s: %s</li>", row.label, html.EscapeString(row.val))
				}
			}
			b.WriteString("</ul>")
		}
	}

	if submission.Message != "" {
		b.WriteString("<h3>Message (as submitted)</h3>")
		fmt.Fprintf(b, "<p>%s</p>", html.EscapeString(submission.Message))
	}
	if len(submission.Answers) > 0 {
		b.WriteString("<h3>Screening questions (as answered on the Apply form)</h3><ol>")
		for _, a := range submission.Answers {
			fmt.Fprintf(b, "<li>%s<br>%s</li>", html.EscapeString(a.Question), html.EscapeString(a.Answer))
		}
		b.WriteString("</ol>")
	}
}

// applicationEmailHTML composes the packet the account holder reviews. When `submission` is
// present it leads the email -- the name/email/phone and screening answers the account holder
// actually typed into the Apply form -- followed by the job, the drafted cover letter (if any),
// and a note about whether a CV is attached; the employer's own published questions (`form`)
// still render below as reference for what the real posting asks. With no submission the packet
// is exactly what it always was: job, drafted letter, CV note, and the employer's questions.
// Plain, readable HTML rather than a template file -- this is a one-recipient utility email, not
// a branded surface. Every field pulled from the posting, the ATS form, or the submission is
// HTML-escaped; only the static copy below is not.
func applicationEmailHTML(job db.Job, form *applyform.Display, letterBody string, cvAttached bool, submission *jobtracking.ApplySubmission) string {
	var b strings.Builder

	fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(job.Title))
	fmt.Fprintf(&b, "<p><strong>%s</strong></p>", html.EscapeString(job.Company))
	if job.URL != "" {
		fmt.Fprintf(&b, `<p>Real posting (on the employer's own ATS): <a href="%s">%s</a></p>`,
			html.EscapeString(job.URL), html.EscapeString(job.URL))
	}
	fmt.Fprintf(&b, "<p>Listed on HireAll: /jobs/%s</p>", html.EscapeString(job.PublicSlug))

	if submission != nil {
		writeSubmissionSections(&b, submission)
	}

	if cvAttached {
		b.WriteString("<p>Your tailored CV for this job is attached as cv.pdf.</p>")
	} else {
		b.WriteString("<p><em>No tailored CV found for this job -- attach one yourself before forwarding, or tailor one in HireAll first.</em></p>")
	}

	if letterBody != "" {
		b.WriteString("<h3>Cover letter (as drafted in HireAll)</h3>")
		fmt.Fprintf(&b, "<p>%s</p>", html.EscapeString(letterBody))
	}

	if form != nil {
		fmt.Fprintf(&b, "<h3>The employer's own application form, as published on %s</h3>", html.EscapeString(form.Provider))
		if len(form.Basics) > 0 {
			fmt.Fprintf(&b, "<p>Also asks for: %s</p>", html.EscapeString(strings.Join(form.Basics, ", ")))
		}
		if len(form.Questions) > 0 && (submission == nil || len(submission.Answers) == 0) {
			b.WriteString("<ol>")
			for _, q := range form.Questions {
				req := ""
				if !q.Required {
					req = " (optional)"
				}
				fmt.Fprintf(&b, "<li>%s%s</li>", html.EscapeString(q.Text), req)
			}
			b.WriteString("</ol>")
		}
	} else if submission == nil {
		b.WriteString("<p><em>No screening questions could be read for this posting -- open the real posting link above to see what it asks.</em></p>")
	}

	b.WriteString("<hr><p>This went only to the addresses in APPLY_FORWARD_EMAILS -- never to the employer. Forward it yourself once you've reviewed it.</p>")
	return b.String()
}
