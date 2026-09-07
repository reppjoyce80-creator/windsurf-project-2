<script lang="ts">
  import { CheckCircle2, Plus, X } from '@lucide/svelte';
  import { goto } from '$app/navigation';
  import { api, ApiError } from '$lib/api';
  import type { Display } from '$lib/generated/contracts';
  import type { Job, UserJob } from '$lib/types';
  import { Button, Dialog, Input } from '$lib/ui';

  // Opens on the Apply click and stays open for its whole life -- the parent
  // mounts to open it, unmounts on onClose, same convention as ReportDialog.
  // `applyForm` is the employer's own published screening form (read-only shape,
  // JobApplyForm's `Display`); null for most postings, in which case the form
  // below is everything this application carries.
  //
  // Deliberately shaped like a real ATS application form section by section --
  // personal & contact, work history, education, skills, legal & eligibility --
  // rather than just the three contact fields a screening form alone would need.
  // One form fits every job the same way a real application does; the employer's
  // own questions (when this posting published any) still render as their own
  // section further down, they just aren't the whole form anymore.
  let {
    job,
    applyForm = null,
    onSubmitted,
    onClose,
  }: {
    job: Job;
    applyForm?: Display | null;
    onSubmitted: (interaction: UserJob) => void;
    onClose: () => void;
  } = $props();

  let open = $state(true);
  // Set once the API call succeeds; from then on this is what the dialog shows (a
  // confirmation screen replaces the form) and what closing the dialog hands back to
  // the parent -- see the effect below.
  let submitted = $state<UserJob | null>(null);
  $effect(() => {
    if (!open) {
      if (submitted) onSubmitted(submitted);
      else onClose();
    }
  });

  // The confirmation screen's second exit: closes the dialog (same bookkeeping
  // as Done, via the effect above -- onSubmitted still fires so the underlying
  // job page picks up the "Applied" state) and then leaves the job page entirely
  // for the homepage, which is the fix for "confirmation screen is a dead end":
  // Done alone only ever returns to the job just applied to, never anywhere else.
  function browseMoreJobs() {
    open = false;
    void goto('/');
  }

  // 1. Personal & contact.
  let firstName = $state('');
  let middleName = $state('');
  let lastName = $state('');
  let email = $state('');
  let phone = $state('');
  let city = $state('');
  let region = $state('');
  let postalCode = $state('');
  let country = $state('');

  interface WorkHistoryRow {
    company: string;
    title: string;
    startDate: string;
    endDate: string;
    responsibilities: string;
    reasonForLeaving: string;
  }
  function emptyWorkRow(): WorkHistoryRow {
    return { company: '', title: '', startDate: '', endDate: '', responsibilities: '', reasonForLeaving: '' };
  }
  function hasWorkContent(w: WorkHistoryRow): boolean {
    return Object.values(w).some((v) => v.trim() !== '');
  }

  // 2. Work history, most recent first -- one blank row to start, same as a
  // paper application's first line.
  let workHistory = $state<WorkHistoryRow[]>([emptyWorkRow()]);
  function addWorkHistory() {
    workHistory.push(emptyWorkRow());
  }
  function removeWorkHistory(i: number) {
    workHistory.splice(i, 1);
  }

  interface EducationRow {
    school: string;
    degree: string;
    fieldOfStudy: string;
    graduationYear: string;
  }
  function emptyEducationRow(): EducationRow {
    return { school: '', degree: '', fieldOfStudy: '', graduationYear: '' };
  }
  function hasEducationContent(e: EducationRow): boolean {
    return Object.values(e).some((v) => v.trim() !== '');
  }

  // 3. Education & certifications.
  let education = $state<EducationRow[]>([emptyEducationRow()]);
  function addEducation() {
    education.push(emptyEducationRow());
  }
  function removeEducation(i: number) {
    education.splice(i, 1);
  }
  let certifications = $state('');

  // 4. Skills & qualifications.
  let hardSkills = $state('');
  let languages = $state('');
  let softSkills = $state('');

  // 5. Legal & eligibility disclosures. '' means left blank throughout -- the EEO
  // fields are voluntary self-identification, so '' has to mean "declined to
  // answer" rather than defaulting to some other value.
  let workAuthorized = $state('');
  let needsSponsorship = $state('');
  let eeoRace = $state('');
  let eeoGender = $state('');
  let eeoVeteran = $state('');
  let eeoDisability = $state('');

  let message = $state('');
  // Parallel to applyForm.questions, keyed by index the same way JobApplyForm
  // keys its read-only list -- the questions are the employer's own text and a
  // real ATS form can repeat one verbatim, so position is the only stable identity.
  let answers = $state<string[]>((applyForm?.questions ?? []).map(() => ''));

  let submitting = $state(false);
  let error = $state<string | null>(null);

  // Required questions block submission the same way a real ATS form would --
  // this deployment never contacts the employer, but filling out the form should
  // still feel like the real thing rather than a formality with no teeth. Nothing
  // else on this form is required: a real application form doesn't refuse a
  // candidate who skipped the EEO section either.
  function firstMissingRequired(): string | null {
    const questions = applyForm?.questions ?? [];
    for (let i = 0; i < questions.length; i++) {
      if (questions[i].required && !answers[i]?.trim()) {
        return questions[i].text;
      }
    }
    return null;
  }

  function messageFor(e: unknown): string {
    if (e instanceof ApiError && e.status === 401) {
      return 'Please sign in to apply.';
    }
    return 'Something went wrong sending that. Please try again.';
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    const missing = firstMissingRequired();
    if (missing) {
      error = `Please answer: ${missing}`;
      return;
    }
    error = null;
    submitting = true;
    try {
      const questions = applyForm?.questions ?? [];
      const interaction = await api.submitApplication(job.public_slug, {
        first_name: firstName.trim() || undefined,
        middle_name: middleName.trim() || undefined,
        last_name: lastName.trim() || undefined,
        email: email.trim() || undefined,
        phone: phone.trim() || undefined,
        city: city.trim() || undefined,
        state: region.trim() || undefined,
        postal_code: postalCode.trim() || undefined,
        country: country.trim() || undefined,

        work_history: workHistory
          .filter(hasWorkContent)
          .map((w) => ({
            company: w.company.trim() || undefined,
            title: w.title.trim() || undefined,
            start_date: w.startDate.trim() || undefined,
            end_date: w.endDate.trim() || undefined,
            responsibilities: w.responsibilities.trim() || undefined,
            reason_for_leaving: w.reasonForLeaving.trim() || undefined,
          })),
        education: education
          .filter(hasEducationContent)
          .map((ed) => ({
            school: ed.school.trim() || undefined,
            degree: ed.degree.trim() || undefined,
            field_of_study: ed.fieldOfStudy.trim() || undefined,
            graduation_year: ed.graduationYear.trim() || undefined,
          })),
        certifications: certifications.trim() || undefined,

        hard_skills: hardSkills.trim() || undefined,
        languages: languages.trim() || undefined,
        soft_skills: softSkills.trim() || undefined,

        work_authorized: workAuthorized || undefined,
        needs_sponsorship: needsSponsorship || undefined,
        eeo_race: eeoRace || undefined,
        eeo_gender: eeoGender || undefined,
        eeo_veteran: eeoVeteran || undefined,
        eeo_disability: eeoDisability || undefined,

        message: message.trim() || undefined,
        answers: questions
          .map((q, i) => ({ question: q.text, answer: answers[i]?.trim() ?? '' }))
          .filter((a) => a.answer !== ''),
      });
      submitted = interaction;
    } catch (err) {
      error = messageFor(err);
    } finally {
      submitting = false;
    }
  }

  const fieldClass =
    'resize-y rounded-md border border-border bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
  const selectClass =
    'w-full rounded-md border border-border bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring';
</script>

<Dialog
  bind:open
  title={submitted ? 'Thank You for Applying' : `Apply to ${job.title}`}
  dismissible={!submitting}
  class="sm:max-w-2xl"
>
  {#if submitted}
    <div class="flex flex-col items-center gap-4 px-2 py-6">
      <CheckCircle2 class="size-10 text-brand" aria-hidden="true" />
      <div class="flex max-w-md flex-col gap-3 text-sm">
        <p>
          Thank you for applying for the <strong>{job.title}</strong> position at
          <strong>{job.company}</strong>. We have successfully received your application
          and materials.
        </p>
        <p class="font-medium">What happens next?</p>
        <p class="text-muted-foreground">
          Our hiring team is currently reviewing all submissions. If your qualifications
          match our needs for this role, we will reach out to you within 5 to 7 business
          days to schedule an initial interview.
        </p>
        <p class="text-muted-foreground">
          In the meantime, you can log back into your account on HireAll to track your
          application status.
        </p>
      </div>
      <div class="flex flex-wrap justify-center gap-3">
        <Button type="button" variant="primary" onclick={browseMoreJobs}>Browse more jobs</Button>
        <Button type="button" variant="outline" onclick={() => (open = false)}>Done</Button>
      </div>
    </div>
  {:else}
  <form class="flex flex-col gap-6" onsubmit={submit}>
    <!-- The whole point of this deployment's Apply: fill this out like a real
         application, and it's emailed to the account holder to review and send
         themselves -- it never reaches the employer. (The explanatory line that
         used to render here was removed from the UI itself; the behavior is
         unchanged, see NotifyApplied.) -->

    <!-- 1. Personal & contact -->
    <div class="flex flex-col gap-3">
      <h3 class="text-sm font-semibold">Personal &amp; contact information</h3>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <label class="flex flex-col gap-1.5 text-sm">
          <span class="font-medium">First name</span>
          <Input bind:value={firstName} placeholder="Jane" autocomplete="given-name" />
        </label>
        <label class="flex flex-col gap-1.5 text-sm">
          <span class="font-medium">Middle name</span>
          <Input bind:value={middleName} placeholder="Optional" autocomplete="additional-name" />
        </label>
        <label class="flex flex-col gap-1.5 text-sm">
          <span class="font-medium">Last name</span>
          <Input bind:value={lastName} placeholder="Doe" autocomplete="family-name" />
        </label>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label class="flex flex-col gap-1.5 text-sm">
          <span class="font-medium">Email</span>
          <Input type="email" bind:value={email} placeholder="you@example.com" autocomplete="email" />
        </label>
        <label class="flex flex-col gap-1.5 text-sm">
          <span class="font-medium">Phone</span>
          <Input type="tel" bind:value={phone} placeholder="Optional" autocomplete="tel" />
        </label>
      </div>
      <span class="text-xs text-muted-foreground">Current address (city, state/region and zip are enough)</span>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Input bind:value={city} placeholder="City" autocomplete="address-level2" />
        <Input bind:value={region} placeholder="State / region" autocomplete="address-level1" />
        <Input bind:value={postalCode} placeholder="Zip / postal code" autocomplete="postal-code" />
        <Input bind:value={country} placeholder="Country" autocomplete="country-name" />
      </div>
    </div>

    <!-- 2. Work history -->
    <div class="flex flex-col gap-3 border-t border-border pt-4">
      <div class="flex items-center justify-between">
        <h3 class="text-sm font-semibold">Work history</h3>
        <span class="text-xs text-muted-foreground">Most recent job first</span>
      </div>
      {#each workHistory as row, i (i)}
        <div class="flex flex-col gap-2 rounded-md border border-border p-3">
          <div class="flex items-start justify-between gap-2">
            <div class="grid flex-1 grid-cols-1 gap-2 sm:grid-cols-2">
              <Input bind:value={row.company} placeholder="Company name" autocomplete="organization" />
              <Input bind:value={row.title} placeholder="Job title" autocomplete="organization-title" />
            </div>
            {#if workHistory.length > 1}
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Remove this job"
                onclick={() => removeWorkHistory(i)}
              >
                <X class="size-4" />
              </Button>
            {/if}
          </div>
          <div class="grid grid-cols-2 gap-2">
            <Input bind:value={row.startDate} placeholder="Start (e.g. Jan 2022)" />
            <Input bind:value={row.endDate} placeholder="End (blank = present)" />
          </div>
          <textarea
            bind:value={row.responsibilities}
            rows="2"
            placeholder="Key responsibilities and achievements"
            class={fieldClass}
          ></textarea>
          <Input bind:value={row.reasonForLeaving} placeholder="Reason for leaving (optional)" />
        </div>
      {/each}
      <Button type="button" variant="outline" size="sm" class="self-start" onclick={addWorkHistory}>
        <Plus class="size-3.5" />
        Add another job
      </Button>
    </div>

    <!-- 3. Education & certifications -->
    <div class="flex flex-col gap-3 border-t border-border pt-4">
      <h3 class="text-sm font-semibold">Education &amp; certifications</h3>
      {#each education as row, i (i)}
        <div class="flex flex-col gap-2 rounded-md border border-border p-3">
          <div class="flex items-start justify-between gap-2">
            <div class="grid flex-1 grid-cols-1 gap-2 sm:grid-cols-2">
              <Input bind:value={row.school} placeholder="School name" autocomplete="organization" />
              <Input bind:value={row.degree} placeholder="Degree (e.g. B.S.)" />
            </div>
            {#if education.length > 1}
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Remove this school"
                onclick={() => removeEducation(i)}
              >
                <X class="size-4" />
              </Button>
            {/if}
          </div>
          <div class="grid grid-cols-2 gap-2">
            <Input bind:value={row.fieldOfStudy} placeholder="Major / field of study" />
            <Input bind:value={row.graduationYear} placeholder="Graduation year" />
          </div>
        </div>
      {/each}
      <Button type="button" variant="outline" size="sm" class="self-start" onclick={addEducation}>
        <Plus class="size-3.5" />
        Add another school
      </Button>
      <label class="flex flex-col gap-1.5 text-sm">
        <span class="font-medium">Professional licenses &amp; certifications</span>
        <textarea
          bind:value={certifications}
          rows="2"
          placeholder="Any active industry certifications or training relevant to this job"
          class={fieldClass}
        ></textarea>
      </label>
    </div>

    <!-- 4. Skills & qualifications -->
    <div class="flex flex-col gap-3 border-t border-border pt-4">
      <h3 class="text-sm font-semibold">Skills &amp; qualifications</h3>
      <label class="flex flex-col gap-1.5 text-sm">
        <span class="font-medium">Hard skills</span>
        <span class="text-xs text-muted-foreground">Software, tools, or technical abilities -- comma-separated</span>
        <Input bind:value={hardSkills} placeholder="Python, Excel, Salesforce" />
      </label>
      <label class="flex flex-col gap-1.5 text-sm">
        <span class="font-medium">Languages spoken</span>
        <Input bind:value={languages} placeholder="English (native), Spanish (conversational)" />
      </label>
      <label class="flex flex-col gap-1.5 text-sm">
        <span class="font-medium">Soft skills</span>
        <textarea
          bind:value={softSkills}
          rows="2"
          placeholder="Communication, leadership, time management…"
          class={fieldClass}
        ></textarea>
      </label>
    </div>

    <!-- 5. Legal & eligibility disclosures -->
    <div class="flex flex-col gap-3 border-t border-border pt-4">
      <h3 class="text-sm font-semibold">Legal &amp; eligibility</h3>
      <fieldset class="flex flex-col gap-1.5 text-sm">
        <legend class="font-medium">Are you legally authorized to work in this job's country?</legend>
        <div class="flex gap-4">
          <label class="flex items-center gap-1.5">
            <input type="radio" name="workAuthorized" value="yes" bind:group={workAuthorized} />
            Yes
          </label>
          <label class="flex items-center gap-1.5">
            <input type="radio" name="workAuthorized" value="no" bind:group={workAuthorized} />
            No
          </label>
        </div>
      </fieldset>
      <fieldset class="flex flex-col gap-1.5 text-sm">
        <legend class="font-medium">Will you now or in the future require visa sponsorship?</legend>
        <div class="flex gap-4">
          <label class="flex items-center gap-1.5">
            <input type="radio" name="needsSponsorship" value="yes" bind:group={needsSponsorship} />
            Yes
          </label>
          <label class="flex items-center gap-1.5">
            <input type="radio" name="needsSponsorship" value="no" bind:group={needsSponsorship} />
            No
          </label>
        </div>
      </fieldset>

      <div class="flex flex-col gap-2 rounded-md border border-border p-3">
        <p class="text-xs text-muted-foreground">
          The following is voluntary self-identification, used only for the equal-opportunity
          section a real application form includes. Leave any of these on "Decline to self-identify"
          -- it changes nothing about this application.
        </p>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium">Race / ethnicity</span>
            <select bind:value={eeoRace} class={selectClass}>
              <option value="">Decline to self-identify</option>
              <option value="American Indian or Alaska Native">American Indian or Alaska Native</option>
              <option value="Asian">Asian</option>
              <option value="Black or African American">Black or African American</option>
              <option value="Hispanic or Latino">Hispanic or Latino</option>
              <option value="Native Hawaiian or Other Pacific Islander"
                >Native Hawaiian or Other Pacific Islander</option
              >
              <option value="White">White</option>
              <option value="Two or more races">Two or more races</option>
            </select>
          </label>
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium">Gender</span>
            <select bind:value={eeoGender} class={selectClass}>
              <option value="">Decline to self-identify</option>
              <option value="Female">Female</option>
              <option value="Male">Male</option>
              <option value="Non-binary">Non-binary</option>
            </select>
          </label>
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium">Veteran status</span>
            <select bind:value={eeoVeteran} class={selectClass}>
              <option value="">Decline to self-identify</option>
              <option value="I am not a protected veteran">I am not a protected veteran</option>
              <option value="I identify as a protected veteran">I identify as a protected veteran</option>
            </select>
          </label>
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium">Disability status</span>
            <select bind:value={eeoDisability} class={selectClass}>
              <option value="">Decline to self-identify</option>
              <option value="Yes, I have a disability">Yes, I have a disability</option>
              <option value="No, I do not have a disability">No, I do not have a disability</option>
            </select>
          </label>
        </div>
      </div>
    </div>

    {#if applyForm?.questions?.length}
      <div class="flex flex-col gap-3 border-t border-border pt-4">
        <h3 class="text-sm font-semibold">
          This posting's own screening questions
          <span class="font-normal text-muted-foreground">
            -- as published by <span class="capitalize">{applyForm.provider}</span>
          </span>
        </h3>
        {#each applyForm.questions as question, i (i)}
          <label class="flex flex-col gap-1.5 text-sm">
            <span class="font-medium">
              {question.text}{#if question.required}<span class="text-destructive"> *</span>{/if}
            </span>
            {#if question.answer}
              <span class="text-xs text-muted-foreground">Expects: {question.answer}</span>
            {/if}
            <textarea bind:value={answers[i]} rows="2" class={fieldClass}></textarea>
          </label>
        {/each}
      </div>
    {/if}

    <label class="flex flex-col gap-1.5 border-t border-border pt-4 text-sm">
      <span class="font-medium">Anything else to include</span>
      <span class="text-xs text-muted-foreground">
        Optional -- a cover note, or context for this application.
      </span>
      <textarea
        bind:value={message}
        rows="4"
        placeholder="Anything you want the email to carry…"
        class={fieldClass}
      ></textarea>
    </label>

    {#if error}
      <p role="alert" class="text-sm text-destructive">{error}</p>
    {/if}

    <Button type="submit" variant="primary" disabled={submitting}>
      {submitting ? 'Sending…' : 'Submit application'}
    </Button>
  </form>
  {/if}
</Dialog>
