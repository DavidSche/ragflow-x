import { useEffect, useState } from "react";
import { useNotify } from "ra-core";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Shell } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { DEFAULT_BRAND_NAME, getBranding, loadBranding, setBranding } from "../../lib/branding";
import { LogoPickerRow } from "../../components/LogoPickerRow";

interface Envelope<T> {
  code: number;
  message: string;
  data: T;
}

const brandName = (name: string) => name || "RAGFlow-X";

export const BrandingPage = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [name, setName] = useState("");
  const [logo, setLogo] = useState("");

  useEffect(() => {
    // loadBranding() is async (tenant -> platform -> default fallback chain), so
    // the form is seeded after it settles; otherwise the saved branding would
    // be overwritten on screen by the hard-coded default on first visit.
    let alive = true;
    void loadBranding().then(() => {
      if (!alive) return;
      const b = getBranding();
      setName(b.name === DEFAULT_BRAND_NAME ? "" : b.name);
      setLogo(b.logo);
    });
    return () => {
      alive = false;
    };
  }, []);

  const save = async () => {
    try {
      const res = await api.put<Envelope<{ name: string; logo: string }>>(
        "/branding",
        { name, logo },
      );
      setBranding(res.data.data);
      setName(res.data.data.name);
      setLogo(res.data.data.logo);
      notify(t("branding.saved"), { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("branding.save_fail"), {
        type: "error",
      });
    }
  };

  const displayName = brandName(name);

  return (
    <div className="grid gap-6 lg:grid-cols-2" aria-label={t("branding.title")}>
      <div className="space-y-6">
        <div className="space-y-2">
          <Label>{t("branding.system_name")}</Label>
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={128}
            placeholder="RAGFlow-X"
          />
        </div>
        <div className="space-y-2">
          <Label>{t("branding.logo")}</Label>
          <LogoPickerRow value={logo} onChange={setLogo} />
        </div>
        <div className="flex justify-end">
          <Button onClick={save} aria-label={t("branding.save_label")}>{t("branding.save")}</Button>
        </div>
      </div>
      <div className="space-y-4">
        <Preview title={t("branding.preview_sidebar")} logo={logo} name={displayName} sidebar />
        <Preview title={t("branding.preview_login")} logo={logo} name={displayName} centered />
      </div>
    </div>
  );
};

function BrandBar({ logo, name }: { logo: string; name: string }) {
  return (
    <div className="flex items-center gap-2 px-3 py-2">
      {/* Mirrors the real sidebar lockup: 28px tall, width by aspect ratio. */}
      {logo ? (
        <img src={logo} alt={name} className="h-7 w-auto max-w-28 shrink-0 object-contain" />
      ) : (
        <Shell className="size-7" />
      )}
      <span className="min-w-0 truncate text-base font-semibold">{name}</span>
    </div>
  );
}

function Preview({
  title,
  logo,
  name,
  sidebar,
  centered,
}: {
  title: string;
  logo: string;
  name: string;
  sidebar?: boolean;
  centered?: boolean;
}) {
  return (
    <div className="rounded-md border p-4">
      <p className="mb-3 text-xs text-muted-foreground">{title}</p>
      <div className="rounded-md border bg-background">
        <div
          className={
            sidebar
              ? "border-b bg-sidebar text-sidebar-foreground"
              : "border-b bg-zinc-100 dark:bg-zinc-800"
          }
        >
          <BrandBar logo={logo} name={name} />
        </div>
        {centered && (
          <div className="flex justify-center p-6">
            <div className="flex flex-col items-center gap-2">
              {/* Mirrors the login lockup: 40px tall, width by aspect ratio. */}
              {logo ? (
                <img
                  src={logo}
                  alt={name}
                  className="h-10 w-auto max-w-[220px] shrink-0 object-contain"
                />
              ) : (
                <Shell className="size-8" />
              )}
              <span className="min-w-0 max-w-full break-words text-lg font-semibold">{name}</span>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
