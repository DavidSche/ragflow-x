import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface MemoryPolicy {
  id: string;
  assistant_id: string;
  state: string;
  policy_generation: number;
  consent_version: number;
  updated_at: string;
}

interface MemoryPolicyQualityGate {
  run_id: string;
  gate_status: string;
  pass?: boolean | null;
  metrics?: Record<string, unknown>;
  evidence_url?: string;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

export const SelfMemoryPolicyPanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "memory-policy", action: "execute" });
  const { canAccess: canAccessEvidence } = useCanAccess({ resource: "release-governance", action: "read" });
  const [loading, setLoading] = useState(false);
  const [policies, setPolicies] = useState<MemoryPolicy[]>([]);
  const [assistantID, setAssistantID] = useState("");
  const [busy, setBusy] = useState(false);
  const [qualityGates, setQualityGates] = useState<Record<string, MemoryPolicyQualityGate>>({});

  const load = useCallback(async () => {
    if (!canAccess) return;
    setLoading(true);
    try {
      const data = await unwrap(api.get<ApiEnvelope<PageEnvelope<MemoryPolicy>>>("/self/memory-policies"));
      setPolicies(data.items ?? []);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPolicy.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [canAccess, notify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (policies.length === 0) return;
    let cancelled = false;
    const loadQualityGates = async () => {
      const results = await Promise.all(policies.map(async (policy) => {
        try {
          const data = await unwrap(api.get<ApiEnvelope<MemoryPolicyQualityGate>>(
            `/memory-policies/${policy.id}/quality-gate`,
          ));
          return [policy.id, data] as const;
        } catch {
          return [policy.id, null] as const;
        }
      }));
      if (cancelled) return;
      const summaries = Object.fromEntries(
        results.filter((entry): entry is readonly [string, MemoryPolicyQualityGate] => entry[1] !== null),
      );
      setQualityGates(summaries);
    };
    void loadQualityGates();
    return () => {
      cancelled = true;
    };
  }, [policies]);

  const createPolicy = async () => {
    if (!assistantID.trim()) {
      notify(t("memoryPolicy.assistant_required"), { type: "error" });
      return;
    }
    setBusy(true);
    try {
      await api.post("/self/memory-policies", { assistant_id: assistantID.trim() });
      notify(t("memoryPolicy.created"), { type: "success" });
      setAssistantID("");
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPolicy.create_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const changeConsent = async (policy: MemoryPolicy, consent: boolean) => {
    setBusy(true);
    try {
      await api.post(`/memory-policies/${policy.id}/${consent ? "opt-in" : "opt-out"}`);
      notify(t(consent ? "memoryPolicy.opted_in" : "memoryPolicy.opted_out"), { type: "success" });
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("memoryPolicy.consent_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  if (!canAccess) return null;

  return (
    <Card className="mb-4">
      <CardHeader>
        <CardTitle>{t("memoryPolicy.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input value={assistantID} onChange={(event) => setAssistantID(event.target.value)} placeholder={t("memoryPolicy.assistant_placeholder")} aria-label={t("memoryPolicy.assistant_placeholder")} />
          <Button type="button" onClick={createPolicy} disabled={busy}>{t("memoryPolicy.create")}</Button>
        </div>
        {loading ? <div className="text-sm text-muted-foreground">{t("common.loading")}</div> : policies.length === 0 ? (
          <div className="text-sm text-muted-foreground">{t("memoryPolicy.empty")}</div>
        ) : (
          <div className="space-y-2">
            {policies.map((policy) => (
              <div key={policy.id} className="flex flex-wrap items-center gap-2 rounded border p-2 text-sm">
                <span className="font-medium">{policy.assistant_id}</span>
                <span>{policy.state}</span>
                <span>{t("memoryPolicy.generation")}: {policy.policy_generation}</span>
                <span>{t("memoryPolicy.consent")}: {policy.consent_version}</span>
                {(() => {
                  const quality = qualityGates[policy.id];
                  if (!quality) {
                    return <Badge variant="secondary">{t("memoryPolicy.quality_not_evaluated")}</Badge>;
                  }
                  const pairCount = typeof quality.metrics?.pair_count === "number" ? quality.metrics.pair_count : null;
                  const hitLift = typeof quality.metrics?.hit_lift_pp === "number" ? quality.metrics.hit_lift_pp : null;
                  return (
                    <>
                      <Badge variant={quality.gate_status === "PASS" ? "default" : quality.gate_status === "FAIL" ? "destructive" : "secondary"}>
                        {quality.gate_status}
                      </Badge>
                      {pairCount !== null ? <span>{t("memoryPolicy.pairs")}: {pairCount}</span> : null}
                      {hitLift !== null ? <span>{t("memoryPolicy.hit_lift")}: {hitLift}pp</span> : null}
                      {quality.evidence_url && canAccessEvidence ? (
                        <a href={quality.evidence_url} target="_blank" rel="noreferrer" className="underline">
                          {t("memoryPolicy.evidence")}
                        </a>
                      ) : null}
                    </>
                  );
                })()}
                <Button size="sm" variant="outline" onClick={() => changeConsent(policy, true)} disabled={busy}>{t("memoryPolicy.opt_in")}</Button>
                <Button size="sm" variant="outline" onClick={() => changeConsent(policy, false)} disabled={busy}>{t("memoryPolicy.opt_out")}</Button>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
};
