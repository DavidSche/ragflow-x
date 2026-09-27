/**
 * DatasetShow – dataset detail page with documents and configuration panels.
 *
 * Sub-components are split into:
 * - dataset-types.ts       (types, utils)
 * - ConfigurationPanel.tsx  (dataset config editor)
 * - DocumentsPanel.tsx      (document list, upload, parse, chunks)
 */
import { Show } from "@/components/admin";
import { useRecordContext, useTranslate } from "ra-core";
import { ConfigurationPanel } from "./ConfigurationPanel";
import { DocumentsPanel } from "./DocumentsPanel";

export const DatasetShow = () => {
  const t = useTranslate();
  return (
  <Show aria-label={t("datasets.aria_dataset_detail")}>
    <DatasetDetail />
  </Show>
  );
};

const DatasetDetail = () => {
  const record = useRecordContext<{ name?: string }>();
  const t = useTranslate();
  return (
    <div className="space-y-2">
      <div className="text-sm text-muted-foreground">
        {t("datasets.name")}：{record?.name ?? "-"}
      </div>
      <DocumentsPanel />
      <ConfigurationPanel />
    </div>
  );
};
