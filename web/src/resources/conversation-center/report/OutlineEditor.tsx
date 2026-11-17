/**
 * Report outline editor (doc/288 §11.1 + §5.6, T9).
 *
 * The outline shown to the reviewer is the content template's declared section
 * set: the reviewer may edit titles, but required sections can never be removed
 * (P0-A enforces `outline_confirmation: required`, so a deletion would let the
 * report silently skip a mandated section). Confirmation POSTs the edited titles
 * to `POST /reports/:id/revisions/:revision/outline/confirm` (202 accepted).
 */
import { useEffect, useMemo, useState } from "react";
import { useTranslate } from "ra-core";
import { Check, FileText, Lock, PenLine } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { ReportTemplateSection } from "./report-run";
import type { ReportOutlineMode } from "./report-contract";

export interface OutlineSectionDraft {
  section_key: string;
  title: string;
  requires_evidence: boolean;
}

export interface OutlineEditorProps {
  reportId: string;
  revision: number;
  mode: ReportOutlineMode;
  /** Template-declared sections, edited in place. */
  sections: ReportTemplateSection[];
  /** Keys the reviewer may not remove (P0-A: every template section). */
  requiredKeys?: string[];
  /** True once the revision moved past the confirmation gate. */
  confirmed?: boolean;
  busy?: boolean;
  onConfirm: (sections: OutlineSectionDraft[]) => Promise<void> | void;
}

export const requiredSectionKeys = (sections: ReportTemplateSection[]): string[] =>
  sections.map((section) => section.key);

export function OutlineEditor({
  reportId,
  revision,
  mode,
  sections,
  requiredKeys,
  confirmed = false,
  busy = false,
  onConfirm,
}: OutlineEditorProps) {
  const t = useTranslate();
  const required = useMemo(
    () => new Set(requiredKeys ?? requiredSectionKeys(sections)),
    [requiredKeys, sections],
  );
  const [drafts, setDrafts] = useState<OutlineSectionDraft[]>(() =>
    sections.map((section) => ({
      section_key: section.key,
      title: section.title,
      requires_evidence: section.requires_evidence,
    })),
  );

  useEffect(() => {
    setDrafts(
      sections.map((section) => ({
        section_key: section.key,
        title: section.title,
        requires_evidence: section.requires_evidence,
      })),
    );
  }, [sections]);

  const editedCount = useMemo(
    () =>
      drafts.filter(
        (draft, index) => draft.title.trim() !== (sections[index]?.title ?? "").trim(),
      ).length,
    [drafts, sections],
  );

  const dirty = drafts.some((draft) => !draft.title.trim());

  const updateTitle = (key: string, title: string) => {
    setDrafts((previous) =>
      previous.map((draft) => (draft.section_key === key ? { ...draft, title } : draft)),
    );
  };

  const remove = (key: string) => {
    if (required.has(key)) return; // 必填节不可删
    setDrafts((previous) =>
      previous.filter((draft) => draft.section_key !== key),
    );
  };

  const submit = () => {
    if (dirty || busy || confirmed) return;
    void onConfirm(
      drafts.map(({ section_key, title, requires_evidence }) => ({
        section_key,
        title: title.trim(),
        requires_evidence,
      })),
    );
  };

  const confirmLabel =
    mode === "auto"
      ? t("conversationCenter.report_outline_auto")
      : t("conversationCenter.report_outline_confirm");

  return (
    <section className="space-y-3" data-testid="outline-editor">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <FileText className="size-4 text-muted-foreground" />
          <h3 className="text-sm font-medium">
            {t("conversationCenter.report_outline_title")}
          </h3>
          <Badge variant="secondary">
            {t("conversationCenter.report_outline_section_count", {
              count: sections.length,
            })}
          </Badge>
        </div>
        {editedCount > 0 && !confirmed && (
          <Badge variant="outline" className="text-xs">
            {t("conversationCenter.report_outline_edited", {
              count: editedCount,
            })}
          </Badge>
        )}
      </div>

      <ul className="space-y-2" data-testid="outline-sections">
        {drafts.map((draft) => {
          const locked = required.has(draft.section_key);
          return (
            <li
              key={draft.section_key}
              className="flex items-center gap-2 rounded-md border bg-card p-2"
              data-testid={`outline-section-${draft.section_key}`}
            >
              <span className="min-w-0 flex-1">
                <Input
                  value={draft.title}
                  onChange={(event) =>
                    updateTitle(draft.section_key, event.target.value)
                  }
                  disabled={confirmed || busy}
                  aria-label={draft.section_key}
                  className="h-9"
                />
              </span>
              {draft.requires_evidence && (
                <Badge variant="secondary" className="whitespace-nowrap text-[10px]">
                  {t("conversationCenter.report_outline_evidence")}
                </Badge>
              )}
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-8"
                    disabled={locked || confirmed || busy}
                    onClick={() => remove(draft.section_key)}
                    aria-label={t("conversationCenter.report_outline_remove")}
                  >
                    {locked ? (
                      <Lock className="size-4 text-muted-foreground/60" />
                    ) : (
                      <PenLine className="size-4" />
                    )}
                  </Button>
                </TooltipTrigger>
                <TooltipContent>
                  {locked
                    ? t("conversationCenter.report_outline_locked")
                    : t("conversationCenter.report_outline_remove")}
                </TooltipContent>
              </Tooltip>
            </li>
          );
        })}
      </ul>

      <div className="flex items-center justify-end gap-2 border-t pt-3">
        {mode === "auto" && (
          <span className="mr-auto text-xs text-muted-foreground">
            {t("conversationCenter.report_outline_auto_hint")}
          </span>
        )}
        {confirmed ? (
          <Badge variant="outline" className="text-emerald-600 dark:text-emerald-400">
            <Check className="mr-1 size-3" />
            {t("conversationCenter.report_outline_confirmed")}
          </Badge>
        ) : (
          <Button onClick={submit} disabled={dirty || busy}>
            {busy
              ? t("conversationCenter.report_outline_confirming")
              : confirmLabel}
          </Button>
        )}
      </div>
    </section>
  );
}
