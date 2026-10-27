import type { FC, SVGProps } from "react";

type IconProps = SVGProps<SVGSVGElement>;

const OpenAIIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <path d="M22.282 9.821a5.985 5.985 0 0 0-.516-4.91 6.046 6.046 0 0 0-6.51-2.9A6.065 6.065 0 0 0 4.981 4.18a5.985 5.985 0 0 0-3.998 2.9 6.046 6.046 0 0 0 .743 7.097 5.98 5.98 0 0 0 .51 4.911 6.051 6.051 0 0 0 6.515 2.9A5.985 5.985 0 0 0 13.26 24a6.056 6.056 0 0 0 5.772-4.206 5.99 5.99 0 0 0 3.997-2.9 6.056 6.056 0 0 0-.747-7.073zM13.26 22.43a4.476 4.476 0 0 1-2.876-1.04l.141-.081 4.779-2.758a.795.795 0 0 0 .392-.681v-6.737l2.02 1.168a.071.071 0 0 1 .038.052v5.583a4.504 4.504 0 0 1-4.494 4.494zM3.6 18.304a4.47 4.47 0 0 1-.535-3.014l.142.085 4.783 2.759a.771.771 0 0 0 .78 0l5.843-3.369v2.332a.08.08 0 0 1-.033.062L9.74 19.95a4.5 4.5 0 0 1-6.14-1.646zM2.34 7.896a4.485 4.485 0 0 1 2.366-1.973V11.6a.766.766 0 0 0 .388.676l5.815 3.355-2.02 1.168a.076.076 0 0 1-.071 0l-4.83-2.786A4.504 4.504 0 0 1 2.34 7.872zm16.597 3.855-5.833-3.387L15.119 7.2a.076.076 0 0 1 .071 0l4.83 2.791a4.494 4.494 0 0 1-.676 8.105v-5.678a.79.79 0 0 0-.407-.667zm2.01-3.023-.141-.085-4.774-2.782a.776.776 0 0 0-.785 0L9.409 9.23V6.897a.066.066 0 0 1 .028-.061l4.83-2.787a4.5 4.5 0 0 1 6.68 4.66zm-12.64 4.135-2.02-1.164a.08.08 0 0 1-.038-.057V6.075a4.5 4.5 0 0 1 7.375-3.453l-.142.08L8.704 5.46a.795.795 0 0 0-.393.681zm1.097-2.365 2.602-1.5 2.607 1.5v2.999l-2.597 1.5-2.607-1.5z" />
  </svg>
);

const OllamaIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <ellipse cx="12" cy="17.1" rx="5.6" ry="5.4" />
    <ellipse cx="6.4" cy="6.5" rx="2.5" ry="3.1" />
    <ellipse cx="17.6" cy="6.5" rx="2.5" ry="3.1" />
  </svg>
);

const VLLMIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <path d="M4 3h3l5 14L17 3h3l-6.5 18h-3L4 3z" />
  </svg>
);

const DeepSeekIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <circle cx="12" cy="12" r="10" opacity={0.18} />
    <path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8zm0-13a5 5 0 1 0 5 5 5 5 0 0 0-5-5zm0 8a3 3 0 1 1 3-3 3 3 0 0 1-3 3z" />
  </svg>
);

const AnthropicIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <path d="M13.83 5h-3.1L4.5 19h3.2l1.17-2.82h6.26L16.3 19h3.2L13.83 5zm-3.9 8.55 2.05-4.94 2.05 4.94H9.93z" />
  </svg>
);

const GenericIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" {...props}>
    <rect x="2" y="4" width="20" height="16" rx="2" />
    <path d="M6 8h4M6 12h8M6 16h5" />
  </svg>
);

const ZhipuIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <path d="M12 2a10 10 0 1 0 10 10A10 10 0 0 0 12 2zm0 18a8 8 0 1 1 8-8 8 8 0 0 1-8 8zM7 9h10v2H7zm0 4h6v2H7z" />
  </svg>
);

const AzureIcon: FC<IconProps> = (props) => (
  <svg viewBox="0 0 24 24" fill="currentColor" {...props}>
    <path d="M13.5 3 6 19h4.2l1.2-3h6.2l1.2 3H22L15 3h-1.5zm-.8 9.5 2-5.2 2 5.2h-4z" />
  </svg>
);

export interface ProviderLogoMeta {
  /** Human-readable brand label shown beside or below the logo. */
  tag: string;
  Icon: FC<IconProps>;
  /** Tailwind color class for the icon. */
  color: string;
}

const LOGOS: Record<string, ProviderLogoMeta> = {
  "OpenAI-API-Compatible": { tag: "OpenAI Compatible", Icon: OpenAIIcon, color: "text-emerald-600" },
  "Azure-OpenAI": { tag: "Azure OpenAI", Icon: AzureIcon, color: "text-sky-600" },
  Ollama: { tag: "Ollama", Icon: OllamaIcon, color: "text-foreground" },
  VLLM: { tag: "vLLM", Icon: VLLMIcon, color: "text-indigo-600" },
  DeepSeek: { tag: "DeepSeek", Icon: DeepSeekIcon, color: "text-blue-600" },
  Moonshot: { tag: "Moonshot", Icon: GenericIcon, color: "text-violet-600" },
  "ZHIPU-AI": { tag: "Zhipu AI", Icon: ZhipuIcon, color: "text-cyan-600" },
  "Tongyi-Qianwen": { tag: "通义千问", Icon: GenericIcon, color: "text-purple-600" },
  Baichuan: { tag: "Baichuan", Icon: GenericIcon, color: "text-rose-600" },
  MiniMax: { tag: "MiniMax", Icon: GenericIcon, color: "text-orange-600" },
  BaiduYiyan: { tag: "Baidu Yiyan", Icon: GenericIcon, color: "text-red-600" },
  TencentHunyuan: { tag: "Tencent Hunyuan", Icon: GenericIcon, color: "text-blue-500" },
  Xinference: { tag: "Xinference", Icon: GenericIcon, color: "text-teal-600" },
  LocalAI: { tag: "LocalAI", Icon: GenericIcon, color: "text-lime-600" },
  Anthropic: { tag: "Anthropic", Icon: AnthropicIcon, color: "text-amber-600" },
  Google: { tag: "Google", Icon: GenericIcon, color: "text-green-600" },
};

/** Put private-deployment–friendly providers first, then alphabetical. */
const ORDER = [
  "OpenAI-API-Compatible",
  "Ollama",
  "VLLM",
  "Xinference",
  "LocalAI",
  "DeepSeek",
  "ZHIPU-AI",
  "Tongyi-Qianwen",
  "Moonshot",
  "Baichuan",
  "MiniMax",
  "BaiduYiyan",
  "TencentHunyuan",
  "Azure-OpenAI",
  "Anthropic",
  "Google",
];

export function sortProviders<T extends { name: string }>(items: T[]): T[] {
  return [...items].sort((a, b) => {
    const ia = ORDER.indexOf(a.name);
    const ib = ORDER.indexOf(b.name);
    if (ia !== -1 && ib !== -1) return ia - ib;
    if (ia !== -1) return -1;
    if (ib !== -1) return 1;
    return a.name.localeCompare(b.name);
  });
}

export function getProviderLogo(name: string): ProviderLogoMeta {
  return (
    LOGOS[name] ?? {
      tag: name,
      Icon: GenericIcon,
      color: "text-muted-foreground",
    }
  );
}
