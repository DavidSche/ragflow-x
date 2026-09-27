/**
 * ChatInput – textarea with attach, voice input, send/stop for the workbench.
 */
import { useEffect, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { CircleAlert, Loader2, Mic, Paperclip, Send, Square, X } from "lucide-react";
import type { AttachmentDraft } from "./workbench-types";

interface ChatInputProps {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  onStop: () => void;
  busy: boolean;
  attachments: AttachmentDraft[];
  onAddFiles: (files: File[]) => void;
  onRemoveFile: (id: string) => void;
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n)) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

type SpeechRecognitionLike = {
  lang: string;
  interimResults: boolean;
  continuous: boolean;
  onresult: ((e: {
    results: ArrayLike<{ isFinal: boolean; [x: number]: { transcript: string } }>;
  }) => void) | null;
  onend: (() => void) | null;
  onerror: (() => void) | null;
  start: () => void;
  stop: () => void;
};

export function ChatInput({
  value,
  onChange,
  onSend,
  onStop,
  busy,
  attachments,
  onAddFiles,
  onRemoveFile,
}: ChatInputProps) {
  const translate = useTranslate();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const anyUploading = attachments.some((a) => a.status === "uploading");

  const [listening, setListening] = useState(false);
  const valueRef = useRef(value);
  const recRef = useRef<SpeechRecognitionLike | null>(null);
  useEffect(() => {
    valueRef.current = value;
  }, [value]);

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const SpeechRecognitionCtor: (new () => SpeechRecognitionLike) | undefined = typeof window !== "undefined"
    ? (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
    : undefined;

  const toggleVoice = () => {
    if (!SpeechRecognitionCtor) return;
    if (listening) {
      recRef.current?.stop();
      return;
    }
    const rec = new SpeechRecognitionCtor();
    rec.lang = "zh-CN";
    rec.interimResults = true;
    rec.continuous = false;
    let finalText = "";
    rec.onresult = (e) => {
      let interim = "";
      for (let i = 0; i < e.results.length; i += 1) {
        const r = e.results[i];
        const t = r[0]?.transcript ?? "";
        if (r.isFinal) finalText += t;
        else interim += t + " ";
      }
      const base = valueRef.current.replace(/\s+$/, "");
      onChange(`${base ? base + " " : ""}${finalText}${interim ? " " + interim : ""}`.trim());
    };
    rec.onend = () => setListening(false);
    rec.onerror = () => setListening(false);
    recRef.current = rec;
    setListening(true);
    try {
      rec.start();
    } catch {
      setListening(false);
    }
  };

  return (
    <div className="space-y-2 rounded border bg-background p-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          size="icon"
          variant="ghost"
          className="h-7 w-7 shrink-0"
          onClick={() => fileInputRef.current?.click()}
          title={translate("workbench.attach")}
          aria-label={translate("workbench.attach")}
        >
          <Paperclip className="size-4" />
        </Button>
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            const files = Array.from(e.target.files ?? []);
            if (files.length) onAddFiles(files);
            e.target.value = "";
          }}
          aria-label={translate("workbench.attach")}
        />
        {attachments.map((a) => (
          <div
            key={a.id}
            className="flex max-w-[14rem] items-center gap-1.5 rounded border bg-muted/40 px-2 py-1 text-xs"
          >
            {a.status === "uploading" ? (
              <Loader2 className="size-3 shrink-0 animate-spin text-muted-foreground" />
            ) : a.status === "error" ? (
              <CircleAlert className="size-3 shrink-0 text-destructive" />
            ) : null}
            <span className="truncate">{a.name}</span>
            <span className="shrink-0 text-muted-foreground">
              {a.status === "error" ? "!" : formatBytes(a.size)}
            </span>
            <Button
              size="icon"
              variant="ghost"
              className="h-4 w-4 shrink-0"
              onClick={() => onRemoveFile(a.id)}
              title={translate("workbench.remove_file")}
              aria-label={translate("workbench.remove_file")}
            >
              <X className="size-3" />
            </Button>
          </div>
        ))}
      </div>

      <div className="flex gap-2">
        <textarea
          className="min-h-[52px] flex-1 resize-y rounded border bg-background p-2 text-sm"
          placeholder={translate("workbench.input_placeholder")}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              onSend();
            }
          }}
          aria-label={translate("workbench.input_label")}
        />
        <div className="flex flex-col justify-end gap-1">
          {SpeechRecognitionCtor ? (
            <Button
              size="icon"
              variant={listening ? "default" : "outline"}
              className={listening ? "text-destructive" : ""}
              onClick={() => void toggleVoice()}
              disabled={busy}
              title={listening ? translate("workbench.stop_voice_input") : translate("workbench.voice_input")}
              aria-label={listening ? translate("workbench.stop_voice_input") : translate("workbench.voice_input")}
            >
              {listening ? <Square className="size-4" /> : <Mic className="size-4" />}
            </Button>
          ) : null}
          {busy ? (
            <Button
              size="icon"
              variant="outline"
              onClick={onStop}
              title={translate("workbench.cancel_generation")}
              aria-label={translate("workbench.cancel_generation")}
            >
              <Square className="size-4" />
            </Button>
          ) : null}
          <Button
            size="icon"
            variant="outline"
            onClick={onSend}
            disabled={busy || anyUploading}
            title={translate("workbench.send")}
            aria-label={translate("workbench.send")}
          >
            {busy ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
          </Button>
        </div>
      </div>
    </div>
  );
}
