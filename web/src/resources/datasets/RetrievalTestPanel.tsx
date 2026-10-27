/**
 * RetrievalTestPanel – interactive retrieval test for one dataset (C2).
 *
 * Runs a one-shot RAGFlow /retrieval proxy call with the tuning knobs the
 * native UI exposes (top N / similarity threshold / vector weight / keyword /
 * rerank) and renders the returned chunks. Every execution is recorded as a
 * knowledge-ops event by the backend, keeping panel debugging inside the
 * governance loop.
 */
import { useCallback, useState } from "react";
import { useGetIdentity, useNotify, useRecordContext, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Loader2, Play, Search } from "lucide-react";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface RetrievalTestChunk {
  id?: string;
  content?: string;
  similarity?: number;
  docnm_kwd?: string;
  document_id?: string;
}

interface RetrievalTestResponse {
  question: string;
  total: number;
  duration_ms: number;
  similarity_threshold: number;
  vector_similarity_weight: number;
  chunks: RetrievalTestChunk[];
}

const MAX_PAGE_SIZE = 30;

export const RetrievalTestPanel = () => {
  const record = useRecordContext<{ id?: string; tenant_id?: string }>();
  const datasetId = record?.id;
  const t = useTranslate();
  const notify = useNotify();
  const { data: identity } = useGetIdentity();
  const identityTenantID = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const recordTenantID = record?.tenant_id;

  const [question, setQuestion] = useState("");
  const [pageSize, setPageSize] = useState("5");
  const [threshold, setThreshold] = useState("0.2");
  const [weight, setWeight] = useState("0.3");
  const [keyword, setKeyword] = useState(false);
  const [rerankID, setRerankID] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<RetrievalTestResponse | null>(null);

  const scopeQuery =
    identityTenantID && recordTenantID && identityTenantID !== recordTenantID
      ? `?scope=specific&tenant_id=${encodeURIComponent(recordTenantID)}`
      : "";
  const run = useCallback(async () => {
    if (!datasetId) return;
    const trimmed = question.trim();
    if (!trimmed) {
      notify(t("datasets.retrieval_test_question_required"), { type: "warning" });
      return;
    }
    const size = Math.floor(Number(pageSize));
    if (!Number.isFinite(size) || size < 1 || size > MAX_PAGE_SIZE) {
      notify(t("datasets.retrieval_test_page_size_invalid", { max: MAX_PAGE_SIZE }), { type: "warning" });
      return;
    }
    const thresholdValue = Number(threshold);
    const weightValue = Number(weight);
    if (
      !Number.isFinite(thresholdValue) || thresholdValue < 0 || thresholdValue > 1 ||
      !Number.isFinite(weightValue) || weightValue < 0 || weightValue > 1
    ) {
      notify(t("datasets.retrieval_test_range_invalid"), { type: "warning" });
      return;
    }
    setRunning(true);
    try {
      const suffix = scopeQuery ? scopeQuery.replace("?", "&") : "";
      const data = await unwrap(
        api.post<ApiEnvelope<RetrievalTestResponse>>(
          `/datasets/${encodeURIComponent(datasetId)}/retrieval-test${suffix}`,
          {
            question: trimmed,
            page_size: size,
            similarity_threshold: thresholdValue,
            vector_similarity_weight: weightValue,
            keyword,
            rerank_id: rerankID.trim() || undefined,
          },
        ),
      );
      setResult(data);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("datasets.retrieval_test_failed"), {
        type: "error",
      });
    } finally {
      setRunning(false);
    }
  }, [datasetId, question, pageSize, threshold, weight, keyword, rerankID, scopeQuery, notify, t]);

  return (
    <Card aria-label={t("datasets.retrieval_test_title")}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <Search className="size-4" aria-hidden />
          {t("datasets.retrieval_test_title")}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1">
          <Label htmlFor="retrieval-test-question">{t("datasets.retrieval_test_question")}</Label>
          <Input
            id="retrieval-test-question"
            value={question}
            onChange={(event) => setQuestion(event.target.value)}
            placeholder={t("datasets.retrieval_test_question_placeholder")}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !running) {
                void run();
              }
            }}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <div className="space-y-1">
            <Label htmlFor="retrieval-test-page-size">{t("datasets.retrieval_test_top_n")}</Label>
            <Input
              id="retrieval-test-page-size"
              type="number"
              min={1}
              max={MAX_PAGE_SIZE}
              value={pageSize}
              onChange={(event) => setPageSize(event.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="retrieval-test-threshold">{t("datasets.retrieval_test_threshold")}</Label>
            <Input
              id="retrieval-test-threshold"
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={threshold}
              onChange={(event) => setThreshold(event.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="retrieval-test-weight">{t("datasets.retrieval_test_weight")}</Label>
            <Input
              id="retrieval-test-weight"
              type="number"
              min={0}
              max={1}
              step={0.05}
              value={weight}
              onChange={(event) => setWeight(event.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="retrieval-test-rerank">{t("datasets.retrieval_test_rerank")}</Label>
            <Input
              id="retrieval-test-rerank"
              value={rerankID}
              onChange={(event) => setRerankID(event.target.value)}
              placeholder={t("datasets.retrieval_test_rerank_placeholder")}
            />
          </div>
        </div>
        <div className="flex items-center justify-between">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox
              id="retrieval-test-keyword"
              checked={keyword}
              onCheckedChange={(checked) => setKeyword(checked === true)}
            />
            {t("datasets.retrieval_test_keyword")}
          </label>
          <Button type="button" onClick={() => void run()} disabled={running}>
            {running ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Play className="size-4" aria-hidden />
            )}
            {running ? t("datasets.retrieval_test_running") : t("datasets.retrieval_test_run")}
          </Button>
        </div>
        {result ? (
          <div className="space-y-2" aria-live="polite">
            <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <Badge variant="secondary">{t("datasets.retrieval_test_hits", { total: result.total })}</Badge>
              <span>
                {t("datasets.retrieval_test_threshold")}: {result.similarity_threshold}
              </span>
              <span>
                {t("datasets.retrieval_test_weight")}: {result.vector_similarity_weight}
              </span>
              <span>{result.duration_ms} ms</span>
            </div>
            {result.chunks.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("datasets.retrieval_test_empty")}</p>
            ) : (
              <ol className="space-y-2">
                {result.chunks.map((chunk, index) => (
                  <li
                    key={chunk.id ?? index}
                    className="rounded-md border border-border bg-background p-2 text-sm"
                  >
                    <div className="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
                      <Badge variant="outline">#{index + 1}</Badge>
                      {typeof chunk.similarity === "number" ? (
                        <span>score: {chunk.similarity.toFixed(4)}</span>
                      ) : null}
                      {chunk.docnm_kwd ? <span className="truncate">{chunk.docnm_kwd}</span> : null}
                    </div>
                    <p className="line-clamp-6 whitespace-pre-wrap break-words">{chunk.content ?? ""}</p>
                  </li>
                ))}
              </ol>
            )}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">{t("datasets.retrieval_test_empty")}</p>
        )}
      </CardContent>
    </Card>
  );
};
