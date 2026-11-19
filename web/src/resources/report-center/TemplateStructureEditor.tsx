/**
 * R19 structure editor — the entry point `PUT /report-templates/:key` never had.
 *
 * What this form is for: before it, a workspace could fork the platform baseline and
 * then never change a section, a prompt or a budget through the UI, so every
 * customisation was authored with `curl` or not at all. The endpoints, the validator
 * and the immutable-version rule all existed; only the authoring surface was missing
 * (doc/288 §8.7 — "有接口无入口").
 *
 * Four decisions the form makes deliberately:
 *
 *  - **it publishes, it never edits in place.** "保存" always mints version N+1 under
 *    the same key, which is what keeps a report pinned to N rendering N. Byte-identical
 *    content comes back `created=false` instead of inflating the version counter: a
 *    version number is an audit coordinate, not a save counter.
 *  - **it validates with the server's rules before sending.** `44015/44105` for a
 *    structural template error is the server's boundary; reaching it means the author
 *    typed a bad number and got a round trip in exchange. The errors print as a list
 *    that names the section, and "保存" stays disabled until the list is empty.
 *  - **numbers are text.** A cleared input is `""`, not `0` — `max_sections=0` is a
 *    legal-looking value that the server would refuse for a different reason.
 *  - **`auto` asks who may skip the review.** Switching outline confirmation off
 *    without naming roles hands the permission to everyone (292 D2) and the server
 *    refuses the payload, so the roles are part of the same decision, not a detail.
 *
 * It also covers the create verb (`POST /report-templates`), which had the same
 * problem one step earlier: forking a platform key was the only way to get a
 * workspace template, so a workspace could never add a report type the platform does
 * not ship. `mode="create"` asks for a key and starts from one empty section.
 */
import { useMemo, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { api, ApiError, unwrap } from "../../lib/api";
import {
  TEMPLATE_AUTO_ROLES,
  TEMPLATE_CONFLICT_HANDLINGS,
  TEMPLATE_OUTLINE_CONFIRMATIONS,
  TEMPLATE_OUTDATED_EVIDENCE,
  TEMPLATE_OUTPUT_FORMATS,
  TEMPLATE_THRESHOLD_POLICIES,
  TEMPLATE_UNSUPPORTED_CLAIMS,
  blankSection,
  draftFromPayload,
  draftToPayload,
  moveSection,
  nextSectionID,
  removeSection,
  templateOptionLabel,
  templateRoleLabel,
  validateTemplateDraft,
  type TemplateDraft,
  type TemplateSectionDraft,
} from "./template-editor";

export interface TemplateStructureEditorProps {
  /** `create` posts a brand-new key; `edit` publishes the next version of `templateKey`. */
  mode: "create" | "edit";
  /** Required for `edit`, absent for `create` (the author types it). */
  templateKey?: string;
  /** The newest row's payload; the draft is seeded from exactly what is published. */
  payload?: unknown;
  onCancel: () => void;
  /**
   * Called with the version the server minted and whether it minted one at all.
   * `created=false` means byte-identical content: the caller must say "nothing new"
   * rather than "saved as vN", because no version number was spent.
   */
  onSaved: (saved: { version: number | undefined; created: boolean }) => void;
}

/**
 * The server's key pattern (`reportTemplateKeyPattern`), so a bad key is a sentence
 * in the dialog rather than `400/44105` after a round trip. Keys travel into export
 * ids and audit details — they are contract, so the shape is checked, not sanitised.
 */
const TEMPLATE_KEY_PATTERN = /^[a-z0-9][a-z0-9._-]{1,62}$/;

function toggle(list: string[], value: string): string[] {
  return list.includes(value) ? list.filter((item) => item !== value) : [...list, value];
}

export function TemplateStructureEditor({
  mode,
  templateKey,
  payload,
  onCancel,
  onSaved,
}: TemplateStructureEditorProps) {
  const t = useTranslate();
  const [key, setKey] = useState(templateKey ?? "");
  const [draft, setDraft] = useState<TemplateDraft>(() => {
    const seeded = draftFromPayload(payload ?? {});
    // A brand-new template starts from one empty section rather than an invalid
    // empty list: "add at least one section" is a rule, not a first step.
    return mode === "create" && seeded.sections.length === 0
      ? { ...seeded, sections: [blankSection("s0")] }
      : seeded;
  });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const update = (patch: Partial<TemplateDraft>) =>
    setDraft((current) => ({ ...current, ...patch }));

  const updateSection = (index: number, patch: Partial<TemplateSectionDraft>) =>
    setDraft((current) => ({
      ...current,
      sections: current.sections.map((section, at) =>
        at === index ? { ...section, ...patch } : section,
      ),
    }));

  const errors = useMemo(() => {
    const found = validateTemplateDraft(draft, t);
    if (mode === "create" && !TEMPLATE_KEY_PATTERN.test(key.trim())) {
      return [t("reportCenter.template_err_key_format"), ...found];
    }
    return found;
  }, [draft, key, mode, t]);
  const canSave = errors.length === 0 && !busy;

  const save = async () => {
    if (!canSave) return;
    setBusy(true);
    setError(null);
    try {
      const body = { key: key.trim(), payload: draftToPayload(draft) };
      const result = await unwrap<{ template?: { version?: number }; created?: boolean }>(
        mode === "create"
          ? api.post("/report-templates", body)
          : api.put(`/report-templates/${encodeURIComponent(key.trim())}`, body),
      );
      onSaved({ version: result?.template?.version, created: result?.created !== false });
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4" data-testid="template-editor">
      {mode === "create" && (
        <div className="space-y-1">
          <label className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t("reportCenter.template_editor_key")}</span>
            <Input
              className="w-64"
              value={key}
              onChange={(event) => setKey(event.target.value)}
              data-testid="template-editor-key"
            />
          </label>
          <p className="text-xs text-muted-foreground">{t("reportCenter.template_editor_key_note")}</p>
        </div>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">{t("reportCenter.template_editor_name_zh")}</span>
          <Input
            value={draft.nameZh}
            onChange={(event) => update({ nameZh: event.target.value })}
            data-testid="template-editor-name-zh"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">{t("reportCenter.template_editor_name_en")}</span>
          <Input
            value={draft.nameEn}
            onChange={(event) => update({ nameEn: event.target.value })}
            data-testid="template-editor-name-en"
          />
        </label>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-xs text-muted-foreground">
          {t("reportCenter.template_editor_output_formats")}
        </legend>
        <div className="flex flex-wrap gap-3">
          {TEMPLATE_OUTPUT_FORMATS.map((format) => (
            <label key={format} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-4"
                checked={draft.outputFormats.includes(format)}
                onChange={() =>
                  update({ outputFormats: toggle(draft.outputFormats, format) })
                }
                data-testid={`template-editor-format-${format}`}
              />
              {templateOptionLabel(t, "format", format)}
            </label>
          ))}
        </div>
      </fieldset>

      <div className="space-y-2">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">{t("reportCenter.template_editor_sections")}</h3>
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              setDraft((current) => ({
                ...current,
                sections: [...current.sections, blankSection(nextSectionID(current.sections))],
              }))
            }
            data-testid="template-editor-add-section"
          >
            {t("reportCenter.template_editor_add_section")}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">{t("reportCenter.template_editor_section_key_note")}</p>

        {draft.sections.map((section, index) => {
          const ord = String(index + 1);
          return (
            <div
              key={section.id}
              className="space-y-2 rounded-md border p-3"
              data-testid={`template-editor-section-${index}`}
            >
              <div className="flex flex-wrap items-end gap-2">
                <span className="text-xs text-muted-foreground">
                  {t("reportCenter.template_editor_section_ord", { ord })}
                </span>
                <label className="flex flex-col gap-1 text-xs">
                  <span className="text-muted-foreground">
                    {t("reportCenter.template_editor_section_key")}
                  </span>
                  <Input
                    className="w-40"
                    value={section.key}
                    onChange={(event) => updateSection(index, { key: event.target.value })}
                    data-testid={`template-editor-section-${index}-key`}
                  />
                </label>
                <label className="flex flex-col gap-1 text-xs">
                  <span className="text-muted-foreground">
                    {t("reportCenter.template_editor_section_title")}
                  </span>
                  <Input
                    className="w-56"
                    value={section.title}
                    onChange={(event) => updateSection(index, { title: event.target.value })}
                    data-testid={`template-editor-section-${index}-title`}
                  />
                </label>
                <label className="flex items-center gap-2 pb-1 text-xs">
                  <input
                    type="checkbox"
                    className="size-4"
                    checked={section.requiresEvidence}
                    onChange={(event) =>
                      updateSection(index, { requiresEvidence: event.target.checked })
                    }
                    data-testid={`template-editor-section-${index}-evidence`}
                  />
                  {t("reportCenter.template_editor_section_requires_evidence")}
                </label>
                <label className="flex flex-col gap-1 text-xs">
                  <span className="text-muted-foreground">
                    {t("reportCenter.template_editor_section_max_words")}
                  </span>
                  <Input
                    className="w-20"
                    inputMode="numeric"
                    value={section.maxWords}
                    onChange={(event) => updateSection(index, { maxWords: event.target.value })}
                    data-testid={`template-editor-section-${index}-max-words`}
                  />
                </label>
                <div className="ml-auto flex gap-1">
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      setDraft((current) => ({
                        ...current,
                        sections: moveSection(current.sections, index, -1),
                      }))
                    }
                    disabled={index === 0}
                    data-testid={`template-editor-section-${index}-up`}
                  >
                    {t("reportCenter.template_editor_move_up")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      setDraft((current) => ({
                        ...current,
                        sections: moveSection(current.sections, index, 1),
                      }))
                    }
                    disabled={index === draft.sections.length - 1}
                    data-testid={`template-editor-section-${index}-down`}
                  >
                    {t("reportCenter.template_editor_move_down")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() =>
                      setDraft((current) => ({
                        ...current,
                        sections: removeSection(current.sections, index),
                      }))
                    }
                    data-testid={`template-editor-section-${index}-remove`}
                  >
                    {t("reportCenter.template_editor_remove_section")}
                  </Button>
                </div>
              </div>

              <label className="flex flex-col gap-1 text-xs">
                <span className="text-muted-foreground">
                  {t("reportCenter.template_editor_section_prompt")}
                </span>
                <Textarea
                  rows={3}
                  value={section.prompt}
                  onChange={(event) => updateSection(index, { prompt: event.target.value })}
                  data-testid={`template-editor-section-${index}-prompt`}
                />
              </label>

              <div className="flex flex-wrap items-center gap-3">
                <span className="text-xs text-muted-foreground">
                  {t("reportCenter.template_editor_section_depends_on")}
                </span>
                {draft.sections.length <= 1 ? (
                  <span className="text-xs text-muted-foreground">
                    {t("reportCenter.template_editor_section_depends_none")}
                  </span>
                ) : (
                  draft.sections.map((other, otherIndex) => {
                    const otherKey = other.key.trim();
                    // A section can only wait for a *named* other section; an empty key
                    // is the one being typed and would make the checkbox disappear.
                    if (otherIndex === index || otherKey === "") return null;
                    return (
                      <label key={other.id} className="flex items-center gap-1 text-xs">
                        <input
                          type="checkbox"
                          className="size-3.5"
                          checked={section.dependsOn.includes(other.id)}
                          onChange={() =>
                            updateSection(index, {
                              // Ids, not keys: a renamed target must not unlink.
                              dependsOn: toggle(section.dependsOn, other.id),
                            })
                          }
                          data-testid={`template-editor-section-${index}-dep-${otherKey}`}
                        />
                        {otherKey}
                      </label>
                    );
                  })
                )}
              </div>
            </div>
          );
        })}
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        {(
          [
            ["maxSections", "template_limit_max_sections"],
            ["sliceMaxSeconds", "template_limit_slice_max_seconds"],
            ["totalMaxSeconds", "template_limit_total_max_seconds"],
            ["maxSlices", "template_limit_max_slices"],
            ["maxTokensPerSection", "template_limit_max_tokens_per_section"],
          ] as const
        ).map(([field, label]) => (
          <label key={field} className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t(`reportCenter.${label}`)}</span>
            <Input
              inputMode="numeric"
              value={draft[field]}
              onChange={(event) => update({ [field]: event.target.value } as Partial<TemplateDraft>)}
              data-testid={`template-editor-${field}`}
            />
          </label>
        ))}
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">{t("reportCenter.template_editor_top_k")}</span>
          <Input
            inputMode="numeric"
            value={draft.topK}
            onChange={(event) => update({ topK: event.target.value })}
            data-testid="template-editor-top-k"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">
            {t("reportCenter.template_editor_threshold_policy")}
          </span>
          <select
            className="h-9 rounded-md border bg-transparent px-2 text-sm"
            value={draft.thresholdPolicy}
            onChange={(event) => update({ thresholdPolicy: event.target.value })}
            data-testid="template-editor-threshold-policy"
          >
            {TEMPLATE_THRESHOLD_POLICIES.map((value) => (
              <option key={value} value={value}>
                {templateOptionLabel(t, "threshold", value)}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">
            {t("reportCenter.template_editor_min_evidence")}
          </span>
          <Input
            inputMode="numeric"
            value={draft.minEvidence}
            onChange={(event) => update({ minEvidence: event.target.value })}
            data-testid="template-editor-min-evidence"
          />
        </label>
      </div>

      <div className="grid gap-3 sm:grid-cols-3">
        {(
          [
            [
              "conflictHandling",
              "template_editor_conflict_handling",
              "conflict",
              TEMPLATE_CONFLICT_HANDLINGS,
            ],
            [
              "outdatedEvidence",
              "template_editor_outdated_evidence",
              "outdated",
              TEMPLATE_OUTDATED_EVIDENCE,
            ],
            [
              "unsupportedClaims",
              "template_editor_unsupported_claims",
              "unsupported",
              TEMPLATE_UNSUPPORTED_CLAIMS,
            ],
          ] as const
        ).map(([field, label, group, values]) => (
          <label key={field} className="flex flex-col gap-1 text-xs">
            <span className="text-muted-foreground">{t(`reportCenter.${label}`)}</span>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={draft[field]}
              onChange={(event) => update({ [field]: event.target.value } as Partial<TemplateDraft>)}
              data-testid={`template-editor-${field}`}
            >
              {values.map((value) => (
                <option key={value} value={value}>
                  {templateOptionLabel(t, group, value)}
                </option>
              ))}
            </select>
          </label>
        ))}
      </div>

      <div className="space-y-2">
        <label className="flex flex-col gap-1 text-xs">
          <span className="text-muted-foreground">
            {t("reportCenter.template_editor_outline_confirmation")}
          </span>
          <select
            className="h-9 w-56 rounded-md border bg-transparent px-2 text-sm"
            value={draft.outlineConfirmation}
            onChange={(event) => update({ outlineConfirmation: event.target.value })}
            data-testid="template-editor-outline-confirmation"
          >
            {TEMPLATE_OUTLINE_CONFIRMATIONS.map((value) => (
              <option key={value} value={value}>
                {templateOptionLabel(t, "outline", value)}
              </option>
            ))}
          </select>
        </label>
        {draft.outlineConfirmation === "auto" && (
          <div className="space-y-1">
            <div className="flex flex-wrap gap-3">
              {TEMPLATE_AUTO_ROLES.map((role) => (
                <label key={role} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    className="size-4"
                    checked={draft.autoRoles.includes(role)}
                    onChange={() => update({ autoRoles: toggle(draft.autoRoles, role) })}
                    data-testid={`template-editor-auto-role-${role}`}
                  />
                  {templateRoleLabel(t, role)}
                </label>
              ))}
            </div>
            <p className="text-xs text-muted-foreground">
              {t("reportCenter.template_editor_auto_roles_note")}
            </p>
          </div>
        )}
      </div>

      {errors.length > 0 && (
        <div className="space-y-1" data-testid="template-editor-errors">
          <p role="alert" className="text-xs text-destructive">
            {t("reportCenter.template_editor_invalid", { count: String(errors.length) })}
          </p>
          <ul className="list-disc pl-5 text-xs text-destructive">
            {errors.map((message) => (
              <li key={message}>{message}</li>
            ))}
          </ul>
        </div>
      )}

      {error !== null && (
        <div
          role="alert"
          data-testid="template-editor-error"
          className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive"
        >
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.template_editor_fail")}
        </div>
      )}

      <p className="text-xs text-muted-foreground">{t("reportCenter.template_editor_unchanged_note")}</p>

      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={onCancel} disabled={busy}>
          {t("reportCenter.template_editor_cancel")}
        </Button>
        <Button size="sm" onClick={() => void save()} disabled={!canSave} data-testid="template-editor-save">
          {busy ? <Loader2 className="size-4 animate-spin" /> : null}
          {t("reportCenter.template_editor_save")}
        </Button>
      </div>
    </div>
  );
}
