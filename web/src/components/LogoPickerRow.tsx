import { useRef, type ChangeEvent } from "react";
import { useNotify } from "ra-core";
import { Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, ApiError } from "../lib/api";

interface Envelope<T> {
  code: number;
  message: string;
  data: T;
}

export function LogoPickerRow({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const notify = useNotify();

  const warnIfSmall = (blobUrl: string) => {
    // The lockup renders the logo 28px tall in the sidebar and 40px on the
    // login card; a 2x display therefore needs >= 56px/80px of source height.
    // Below 56px the browser upscales it and it reads as blurry.
    const probe = new Image();
    probe.onload = () => {
      if (probe.naturalHeight > 0 && probe.naturalHeight < 56) {
        notify(
          `Logo 高度仅 ${probe.naturalHeight}px，界面中会放大显示、可能发虚；建议重新上传高度 ≥ 64px 的图片或 SVG`,
          { type: "warning" },
        );
      }
      URL.revokeObjectURL(blobUrl);
    };
    probe.src = blobUrl;
  };

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    if (!/svg/i.test(file.type)) warnIfSmall(URL.createObjectURL(file));
    try {
      const fd = new FormData();
      fd.append("file", file);
      const res = await api.post<Envelope<{ url: string }>>("/branding/logo", fd);
      if (res.data.code === 0) {
        onChange(res.data.data.url);
        notify("Logo 已上传", { type: "success" });
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : "上传失败", { type: "error" });
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button
        type="button"
        size="icon"
        variant="outline"
        title="上传 Logo"
        onClick={() => fileRef.current?.click()}
      >
        <Upload />
      </Button>
      <Input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="或粘贴 Logo URL / 路径"
        maxLength={512}
        className="flex-1"
      />
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={onFile}
      />
      <p className="basis-full text-xs text-muted-foreground">
        推荐 SVG；位图请提供高度 ≥ 64px 的透明 PNG（≤ 2MB）。方形图标与横版字标都可以，
        界面按高度等比缩放，不会被压成方块。
      </p>
    </div>
  );
}
