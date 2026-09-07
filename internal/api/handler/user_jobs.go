package handler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/strelov1/freehire/internal/application/appevent"
	"github.com/strelov1/freehire/internal/application/jobtracking"
	"github.com/strelov1/freehire/internal/engage/reminder"
	"github.com/strelov1/freehire/internal/job/applydate"
	"github.com/strelov1/freehire/internal/platform/db"
)

// trackingHandlers serves the per-user job interactions (view/apply/save/dismiss/
// track), the user-scoped tracking reads (the Kanban listing, slug sets, pipeline,
// swipe deck), and the saved-job reminder controls. The interaction use cases live
// in jobtracking.Service, which also owns the reminder side effect of save/unsave/
// apply — reminder.Service is handed to it as a port, and is held here only for the
// notification-settings endpoints. The cmd/remind worker fires the scheduled
// reminders.
type trackingHandlers struct {
	tracking *jobtracking.Service
	reminder *reminder.Service
	search   searcher
}

func newTrackingHandlers(queries *db.Queries, pool *pgxpool.Pool, search searcher) *trackingHandlers {
	// One reminder service, handed to tracking as its Reminders port and kept here
	// for the notification-settings endpoints. Save/unsave/apply schedule and cancel
	// inside the tracking service, so every caller of it — this handler, the in-app
	// assistant, the mail reconstruction — gets the same side effect.
	rem := reminder.New(reminder.NewQueriesRepository(queries))
	return &trackingHandlers{
		tracking: jobtracking.New(jobtracking.NewQueriesRepository(queries, pool), jobtracking.WithReminders(rem)),
		reminder: rem,
		search:   search,
	}
}

func (h *trackingHandlers) register(api fiber.Router, mw middleware) {
	// Per-user job interactions accept either the session cookie or an API key
	// (RequireAuthOrKey), so a script holding a key can drive the same flow as the
	// browser. Jobs are addressed by their public slug; the handlers resolve it to
	// the internal id before writing user_jobs. All writes are idempotent upserts.
	api.Post("/jobs/:slug/view", mw.key, h.RecordView)
	api.Post("/jobs/:slug/apply", mw.key, h.MarkApplied)
	api.Post("/jobs/:slug/save", mw.key, h.SaveJob)
	api.Delete("/jobs/:slug/save", mw.key, h.UnsaveJob)
	api.Post("/jobs/:slug/dismiss", mw.key, h.DismissJob)
	api.Delete("/jobs/:slug/dismiss", mw.key, h.UndismissJob)
	api.Patch("/jobs/:slug/track", mw.key, h.TrackJob)
	api.Delete("/jobs/:slug/stage", mw.key, h.ClearStage)
	api.Delete("/jobs/:slug/track", mw.key, h.Untrack)

	// The same three writes, addressed by the row id the tracking listing served.
	// The board holds row ids, not slugs, and an application whose posting cmd/prune
	// removed has no slug to hold — so the routes above cannot move its card.
	//
	// Its own namespace rather than /me/tracking/:id: that path is already mounted
	// (see the inbox handlers) addressed by a posting slug, and one segment meaning
	// different things depending on the method is a trap for the next reader.
	//
	// The slug-addressed routes stay: freehire-cli and freehire-mcp name postings.
	api.Patch("/me/applications/:id", mw.key, h.TrackApplication)
	api.Delete("/me/applications/:id", mw.key, h.UntrackApplication)
	api.Delete("/me/applications/:id/stage", mw.key, h.ClearApplicationStage)

	// User-scoped reads live under /me (consistent with /auth/me): the tracking
	// listing joins the caller's interactions with the jobs they touch, viewed-slugs
	// lets the SPA dim already-seen cards without authenticating the public browse
	// list, and analyses lists the jobs the caller has run the AI fit analysis on.
	api.Get("/me/tracking", mw.key, h.ListTrackedJobs)
	api.Get("/me/tracking/viewed", mw.key, h.ListViewedSlugs)
	api.Get("/me/tracking/saved", mw.key, h.ListSavedSlugs)
	api.Get("/me/tracking/dismissed", mw.key, h.ListDismissedSlugs)
	api.Get("/me/tracking/pipeline", mw.key, h.TrackingPipeline)
	api.Get("/me/tracking/swipe", mw.key, h.SwipeDeck)

	// The account-level notification rule (enable, channels) shared by saved-job
	// reminders and both lifecycle nudges. Cookie-only (RequireAuth) like
	// subscriptions — it configures a delivery preference.
	api.Get("/me/notification-settings", mw.cookie, h.GetNotificationSettings)
	api.Put("/me/notification-settings", mw.cookie, h.UpdateNotificationSettings)
}

// interactionResponse is the public shape of a user's interaction with a job. It
// omits user_id (the caller is the user) and carries saved_at/applied_at/stage/
// notes as null until the job is saved, applied to, or tracked.
type interactionResponse struct {
	JobID       int64      `json:"job_id"`
	ViewedAt    *time.Time `json:"viewed_at"`
	SavedAt     *time.Time `json:"saved_at"`
	AppliedAt   *time.Time `json:"applied_at"`
	DismissedAt *time.Time `json:"dismissed_at"`
	Stage       *string    `json:"stage"`
	Notes       *string    `json:"notes"`
}

// trackRequest is the track body: an optional stage and/or notes. A nil field is
// left unchanged by the upsert; at least one must be present.
type trackRequest struct {
	Stage *string `json:"stage"`
	Notes *string `json:"notes"`
}

// toResponse maps the domain Interaction onto the public wire shape.
func toResponse(i jobtracking.Interaction) interactionResponse {
	return interactionResponse{
		JobID: i.JobID, ViewedAt: i.ViewedAt, SavedAt: i.SavedAt,
		AppliedAt: i.AppliedAt, DismissedAt: i.DismissedAt, Stage: i.Stage, Notes: i.Notes,
	}
}

// trackingError maps the jobtracking sentinels onto HTTP statuses. Anything else
// (e.g. a DB failure) falls through to the central RenderError as a 500.
func trackingError(err error) error {
	switch {
	case errors.Is(err, jobtracking.ErrJobNotFound):
		return fiber.NewError(fiber.StatusNotFound, "job not found")
	case errors.Is(err, jobtracking.ErrInvalidStage):
		return fiber.NewError(fiber.StatusBadRequest, "invalid stage")
	case errors.Is(err, jobtracking.ErrInvalidFilter):
		return fiber.NewError(fiber.StatusBadRequest, "filter must be one of: all, viewed, saved, applied, board")
	case errors.Is(err, jobtracking.ErrEmptyTrack):
		return fiber.NewError(fiber.StatusBadRequest, "provide stage and/or notes")
	case errors.Is(err, jobtracking.ErrApplicationNotFound):
		return fiber.NewError(fiber.StatusNotFound, "application not found")
	case errors.Is(err, applydate.ErrOutOfRange):
		// The message names which bound was crossed; it is the service's words, not a second
		// copy of the rule stated here.
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	default:
		return err
	}
}

// RecordView records that the authenticated user viewed a job and returns the
// resulting interaction, including whether they have already applied.
func (h *trackingHandlers) RecordView(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.RecordView(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// applyRequest is the optional body of an apply: either just the day the application was
// actually sent, or the Apply form's own fields (see below), or both, or neither.
//
// AppliedOn is a calendar date rather than a timestamp, read with appliedOnLayout — the same
// layout the ghost report parses, since both take a day from a person. Asking for an instant
// would invite one bearing a timezone, which reads as a different day either side of a border.
//
// The rest mirrors a real ATS application form section by section (see ApplyFormDialog.svelte):
// personal & contact, work history, education, skills, and the legal/eligibility disclosures --
// not just the three fields a screening form alone would need. Every field is optional, same as
// before: absent, they leave the plain undated apply this endpoint has always accepted; present,
// they become a jobtracking.ApplySubmission (see submission below) carried to the ApplyNotifier
// instead of its own reconstruction.
type applyRequest struct {
	AppliedOn string `json:"applied_on"`

	FirstName  string `json:"first_name"`
	MiddleName string `json:"middle_name"`
	LastName   string `json:"last_name"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`

	WorkHistory    []applyWorkHistoryInput `json:"work_history"`
	Education      []applyEducationInput   `json:"education"`
	Certifications string                  `json:"certifications"`

	HardSkills string `json:"hard_skills"`
	Languages  string `json:"languages"`
	SoftSkills string `json:"soft_skills"`

	WorkAuthorized   string `json:"work_authorized"`
	NeedsSponsorship string `json:"needs_sponsorship"`
	EEORace          string `json:"eeo_race"`
	EEOGender        string `json:"eeo_gender"`
	EEOVeteran       string `json:"eeo_veteran"`
	EEODisability    string `json:"eeo_disability"`

	Message string             `json:"message"`
	Answers []applyAnswerInput `json:"answers"`
}

// applyWorkHistoryInput is one prior job as entered on the Apply form.
type applyWorkHistoryInput struct {
	Company          string `json:"company"`
	Title            string `json:"title"`
	StartDate        string `json:"start_date"`
	EndDate          string `json:"end_date"`
	Responsibilities string `json:"responsibilities"`
	ReasonForLeaving string `json:"reason_for_leaving"`
}

// applyEducationInput is one school/credential as entered on the Apply form.
type applyEducationInput struct {
	School         string `json:"school"`
	Degree         string `json:"degree"`
	FieldOfStudy   string `json:"field_of_study"`
	GraduationYear string `json:"graduation_year"`
}

// applyAnswerInput is one screening question and the candidate's own answer to it, as
// submitted from the Apply form.
type applyAnswerInput struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// submission reports whether the request carried any of the applicant's own form
// fields, and builds the jobtracking.ApplySubmission for it if so. A request with none
// of them -- the plain POST /apply the button has always sent, and the only shape the
// assistant tool and mail reconstruction ever produce, since they never go through
// this HTTP door at all -- reports false, leaving the original undated apply untouched.
//
// A work-history or education row where every field is blank is dropped rather than
// carried through: the form always sends whatever rows the candidate added, including
// one they added and then left empty, and an all-blank row in the emailed copy reads
// as a mistake rather than as "nothing here".
func (r applyRequest) submission() (jobtracking.ApplySubmission, bool) {
	has := r.FirstName != "" || r.MiddleName != "" || r.LastName != "" || r.Email != "" ||
		r.Phone != "" || r.City != "" || r.State != "" || r.PostalCode != "" || r.Country != "" ||
		len(r.WorkHistory) > 0 || len(r.Education) > 0 || r.Certifications != "" ||
		r.HardSkills != "" || r.Languages != "" || r.SoftSkills != "" ||
		r.WorkAuthorized != "" || r.NeedsSponsorship != "" ||
		r.EEORace != "" || r.EEOGender != "" || r.EEOVeteran != "" || r.EEODisability != "" ||
		r.Message != "" || len(r.Answers) > 0
	if !has {
		return jobtracking.ApplySubmission{}, false
	}

	workHistory := make([]jobtracking.WorkHistoryEntry, 0, len(r.WorkHistory))
	for _, w := range r.WorkHistory {
		if w.Company == "" && w.Title == "" && w.StartDate == "" && w.EndDate == "" &&
			w.Responsibilities == "" && w.ReasonForLeaving == "" {
			continue
		}
		workHistory = append(workHistory, jobtracking.WorkHistoryEntry{
			Company:          w.Company,
			Title:            w.Title,
			StartDate:        w.StartDate,
			EndDate:          w.EndDate,
			Responsibilities: w.Responsibilities,
			ReasonForLeaving: w.ReasonForLeaving,
		})
	}

	education := make([]jobtracking.EducationEntry, 0, len(r.Education))
	for _, e := range r.Education {
		if e.School == "" && e.Degree == "" && e.FieldOfStudy == "" && e.GraduationYear == "" {
			continue
		}
		education = append(education, jobtracking.EducationEntry{
			School:         e.School,
			Degree:         e.Degree,
			FieldOfStudy:   e.FieldOfStudy,
			GraduationYear: e.GraduationYear,
		})
	}

	answers := make([]jobtracking.ApplyAnswer, 0, len(r.Answers))
	for _, a := range r.Answers {
		if a.Question == "" && a.Answer == "" {
			continue
		}
		answers = append(answers, jobtracking.ApplyAnswer{Question: a.Question, Answer: a.Answer})
	}

	return jobtracking.ApplySubmission{
		FirstName:  r.FirstName,
		MiddleName: r.MiddleName,
		LastName:   r.LastName,
		Email:      r.Email,
		Phone:      r.Phone,
		City:       r.City,
		State:      r.State,
		PostalCode: r.PostalCode,
		Country:    r.Country,

		WorkHistory:    workHistory,
		Education:      education,
		Certifications: r.Certifications,

		HardSkills: r.HardSkills,
		Languages:  r.Languages,
		SoftSkills: r.SoftSkills,

		WorkAuthorized:   r.WorkAuthorized,
		NeedsSponsorship: r.NeedsSponsorship,
		EEORace:          r.EEORace,
		EEOGender:        r.EEOGender,
		EEOVeteran:       r.EEOVeteran,
		EEODisability:    r.EEODisability,

		Message: r.Message,
		Answers: answers,
	}, true
}

// MarkApplied marks a job as applied for the authenticated user and returns the
// updated interaction.
//
// The body is optional in both directions: absent, empty, or carrying no date, the request is
// the undated apply it has always been, stamped now(). A date present is the caller's own
// account of when they applied, and overrides one already recorded.
func (h *trackingHandlers) MarkApplied(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	in, day, err := parseApplyRequest(c)
	if err != nil {
		return err
	}
	// Three paths rejoin immediately after: a filled-in Apply form (submission wins over
	// any date -- the form is filled out and submitted now, not backdated), a stated date
	// with no form, or the plain apply that has always been the default.
	var interaction jobtracking.Interaction
	if submission, ok := in.submission(); ok {
		interaction, err = h.tracking.MarkAppliedWithSubmission(
			c.Context(), userID, c.Params("slug"), appevent.SourceUser, submission)
	} else if day != nil {
		// The believable-date window belongs to the service, so an out-of-range day arrives
		// as an error to render rather than as a rule restated here.
		interaction, err = h.tracking.MarkAppliedOn(
			c.Context(), userID, c.Params("slug"), *day, time.Now().UTC(), appevent.SourceUser)
	} else {
		interaction, err = h.tracking.MarkApplied(c.Context(), userID, c.Params("slug"), appevent.SourceUser)
	}
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// parseApplyRequest reads the optional body of an apply -- both the plain applied_on date
// and (see applyRequest.submission) the Apply form's own fields. The returned day is nil
// whenever the caller sent nothing at all, or a body carrying no `applied_on`: apply has
// always been callable with an empty request, and a client sending none is asking for today
// rather than making a mistake.
//
// Anything else that fails to read is a 400, including a body that is not JSON and one whose
// `applied_on` is not a string. Treating those as "no date" would stamp today for a caller who
// named a day and tell them it worked — the row would then record a different application than
// the one they described, silently. That mattered more than it looks: Fiber's BodyParser also
// fails on a perfectly good JSON body sent without a Content-Type, which is exactly what a curl
// one-liner does.
//
// The day is returned as a day. Placing it at the storage hour is the service's job, because the
// believable-date window is checked against the day and would refuse "today" all morning if it
// were checked against the derived instant.
func parseApplyRequest(c *fiber.Ctx) (applyRequest, *time.Time, error) {
	if len(c.Body()) == 0 {
		return applyRequest{}, nil, nil
	}
	var in applyRequest
	if err := c.BodyParser(&in); err != nil {
		return applyRequest{}, nil, fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if in.AppliedOn == "" {
		return in, nil, nil
	}
	day, err := time.Parse(appliedOnLayout, in.AppliedOn)
	if err != nil {
		return applyRequest{}, nil, fiber.NewError(fiber.StatusBadRequest, "applied_on must be a date like 2026-07-29")
	}
	return in, &day, nil
}

// SaveJob saves (bookmarks) a job for the authenticated user and returns the
// updated interaction. The account's shared notification rule decides whether a
// reminder is scheduled.
func (h *trackingHandlers) SaveJob(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.SaveJob(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// UnsaveJob clears a job's saved mark for the authenticated user. The interaction
// row (view/apply history) survives; if no row exists at all, unsaving is a no-op
// that answers with the zero interaction state — DELETE is idempotent, so "already
// not saved" is success, not an error (the service resolves that case).
func (h *trackingHandlers) UnsaveJob(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.Unsave(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// DismissJob marks a job dismissed (swiped away) for the authenticated user and
// returns the updated interaction. Dismissal only keeps the job out of the swipe
// deck; it stays visible in the public /jobs list and search.
func (h *trackingHandlers) DismissJob(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.Dismiss(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// UndismissJob clears a job's dismissed mark for the authenticated user. The
// interaction row survives; if no row exists at all, undismissing is a no-op that
// answers with the zero interaction state — DELETE is idempotent, so "already not
// dismissed" is success, not an error (the service resolves that case).
func (h *trackingHandlers) UndismissJob(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.Undismiss(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// ClearStage drops a job's pipeline progress (stage and applied_at) for the
// authenticated user while keeping saved_at, viewed_at, and notes intact. Used
// when dragging a Kanban card back to the "Saved" column.
func (h *trackingHandlers) ClearStage(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.ClearProgress(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// Untrack removes a job from the board for the authenticated user: clears
// saved_at, applied_at, stage, and notes while keeping viewed_at so the job
// stays in the user's view history.
func (h *trackingHandlers) Untrack(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}
	interaction, err := h.tracking.Untrack(c.Context(), userID, c.Params("slug"))
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// TrackJob sets the application stage and/or notes for the authenticated user's
// interaction with a job (session cookie or API key). The body is validated by
// the service before the slug lookup, so a bad request never touches the DB: an
// empty body or an unknown stage is a 400. A nil field is left unchanged by the
// upsert. Returns the updated interaction.
func (h *trackingHandlers) TrackJob(c *fiber.Ctx) error {
	userID, err := requireUserID(c)
	if err != nil {
		return err
	}

	var in trackRequest
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	interaction, err := h.tracking.Track(c.Context(), userID, c.Params("slug"), in.Stage, in.Notes, appevent.SourceUser)
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// TrackApplication is TrackJob for a caller naming the row rather than the posting.
// The listing mints two id forms and this accepts both: the application form goes
// straight to the application, the slug form takes the path TrackJob already takes.
func (h *trackingHandlers) TrackApplication(c *fiber.Ctx) error {
	userID, ref, err := applicationRefFor(c)
	if err != nil {
		return err
	}

	var in trackRequest
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}

	var interaction jobtracking.Interaction
	if ref.AppID != 0 {
		interaction, err = h.tracking.TrackApplication(c.Context(), userID, ref.AppID, in.Stage, in.Notes, appevent.SourceUser)
	} else {
		interaction, err = h.tracking.Track(c.Context(), userID, ref.Slug, in.Stage, in.Notes, appevent.SourceUser)
	}
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// UntrackApplication is Untrack addressed by the row id.
func (h *trackingHandlers) UntrackApplication(c *fiber.Ctx) error {
	userID, ref, err := applicationRefFor(c)
	if err != nil {
		return err
	}
	var interaction jobtracking.Interaction
	if ref.AppID != 0 {
		interaction, err = h.tracking.UntrackApplication(c.Context(), userID, ref.AppID)
	} else {
		interaction, err = h.tracking.Untrack(c.Context(), userID, ref.Slug)
	}
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// ClearApplicationStage is ClearStage addressed by the row id.
func (h *trackingHandlers) ClearApplicationStage(c *fiber.Ctx) error {
	userID, ref, err := applicationRefFor(c)
	if err != nil {
		return err
	}
	var interaction jobtracking.Interaction
	if ref.AppID != 0 {
		interaction, err = h.tracking.ClearApplicationProgress(c.Context(), userID, ref.AppID)
	} else {
		interaction, err = h.tracking.ClearProgress(c.Context(), userID, ref.Slug)
	}
	if err != nil {
		return trackingError(err)
	}
	return c.JSON(fiber.Map{"data": toResponse(interaction)})
}

// applicationRefFor authenticates the caller and reads the row id from the path.
func applicationRefFor(c *fiber.Ctx) (int64, applicationRef, error) {
	userID, err := requireUserID(c)
	if err != nil {
		return 0, applicationRef{}, err
	}
	ref, err := parseApplicationRef(c.Params("id"))
	if err != nil {
		return 0, applicationRef{}, err
	}
	return userID, ref, nil
}
