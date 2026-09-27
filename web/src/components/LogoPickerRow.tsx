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

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
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
    <div className="flex items-center gap-2">
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
        className="flex-1"
      />
      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={onFile}
      />
    </div>
  );
}
