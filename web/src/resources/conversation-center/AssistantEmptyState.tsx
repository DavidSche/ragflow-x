import { useTranslate } from "ra-core";

export function AssistantEmptyState() {
  const t = useTranslate();
  return (
    <div className="mb-4 rounded border bg-muted/30 p-4">
      <h2 className="text-lg font-semibold">{t("conversationCenter.empty_question")}</h2>
      <ul className="mt-2 grid gap-1 text-sm text-muted-foreground sm:grid-cols-2">
        <li>{t("conversationCenter.example_1")}</li>
        <li>{t("conversationCenter.example_2")}</li>
        <li>{t("conversationCenter.example_3")}</li>
        <li>{t("conversationCenter.example_4")}</li>
      </ul>
    </div>
  );
}
