import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

export type Language = "zh" | "en";

const messages: Record<Language, Record<string, string>> = {
  zh: {
    "app.name": "RAGFlow-X 管理台",
    "nav.dashboard": "仪表板",
    "nav.tenants": "工作区管理",
    "nav.users": "用户管理",
    "nav.datasets": "数据集",
    "nav.documents": "文档",
    "login.title": "登录 RAGFlow-X",
    "login.username": "用户名",
    "login.password": "密码",
    "login.submit": "登录",
    "dashboard.welcome": "欢迎回来",
    "dashboard.description": "平台概览与资源使用情况",
    "action.create": "新建",
    "action.cancel": "取消",
    "action.confirm": "确认",
    "common.name": "名称",
    "common.status": "状态",
    "common.created": "创建时间",
    "common.actions": "操作",
    "common.empty": "暂无数据",
    "tenant.list": "工作区列表",
    "tenant.create": "创建工作区",
    "tenant.status.active": "启用",
    "tenant.status.disabled": "停用",
    "dataset.list": "数据集列表",
    "dataset.create": "创建数据集",
    "dataset.documents": "查看文档",
    "doc.list": "文档列表",
    "doc.upload": "上传文档",
    "doc.parse": "解析",
    "doc.status.parsed": "已解析",
    "doc.status.pending": "待解析",
    "user.list": "用户列表",
    "user.role": "角色",
    "logout": "退出登录",
    "workbench.title": "问答工作台",
    "workbench.greeting": "你好，选择数据范围后开始提问。",
    "workbench.scope.label": "数据范围",
    "workbench.scope.select": "选择 Chat",
    "workbench.scope.hint": "切换范围会开启新会话；用当前登录身份回答",
    "workbench.scope.empty": "当前没有可用的 Chat（数据范围）。请联系管理员分配，或先在“Chat 管理”中创建。",
    "workbench.generating": "正在生成…",
    "workbench.noHit": "未命中相关内容：当前数据范围内没有找到匹配知识，请尝试调整提问或扩大数据范围。",
    "workbench.timeout": "请求超时，请重试",
    "workbench.error": "问答失败，请重试",
    "workbench.error.429": "请求过于频繁，请稍后重试",
    "workbench.error.403": "权限不足：当前账号无权执行问答，请联系管理员",
    "workbench.feedback.positive": "已标记为有帮助",
    "workbench.feedback.negative": "已标记为需要改进",
    "workbench.feedback.marked.positive": "已标记有帮助",
    "workbench.feedback.marked.negative": "已标记需改进",
    "workbench.feedback.comment.placeholder": "可选：输入改进意见",
    "workbench.feedback.submit": "提交",
    "workbench.copied": "已复制",
    "workbench.copyFailed": "复制失败",
    "workbench.citation.view": "查看原文档",
    "workbench.input.placeholder": "输入问题，Enter 发送（Shift+Enter 换行）",
    "workbench.cancel": "取消生成",
    "workbench.send": "发送",
    "workbench.retry": "重试",
    "workbench.regenerate": "重新生成",
  },
  en: {
    "app.name": "RAGFlow-X Console",
    "nav.dashboard": "Dashboard",
    "nav.tenants": "Workspaces",
    "nav.users": "Users",
    "nav.datasets": "Datasets",
    "nav.documents": "Documents",
    "login.title": "Sign in to RAGFlow-X",
    "login.username": "Username",
    "login.password": "Password",
    "login.submit": "Sign in",
    "dashboard.welcome": "Welcome back",
    "dashboard.description": "Platform overview and resource usage",
    "action.create": "Create",
    "action.cancel": "Cancel",
    "action.confirm": "Confirm",
    "common.name": "Name",
    "common.status": "Status",
    "common.created": "Created",
    "common.actions": "Actions",
    "common.empty": "No data",
    "tenant.list": "Workspaces",
    "tenant.create": "Create workspace",
    "tenant.status.active": "Active",
    "tenant.status.disabled": "Disabled",
    "dataset.list": "Datasets",
    "dataset.create": "Create dataset",
    "dataset.documents": "View documents",
    "doc.list": "Documents",
    "doc.upload": "Upload document",
    "doc.parse": "Parse",
    "doc.status.parsed": "Parsed",
    "doc.status.pending": "Pending",
    "user.list": "Users",
    "user.role": "Role",
    "logout": "Sign out",
    "workbench.title": "Q&A Workbench",
    "workbench.greeting": "Hello. Select a data scope and start asking.",
    "workbench.scope.label": "Data Scope",
    "workbench.scope.select": "Select Chat",
    "workbench.scope.hint": "Switching scope starts a new session; answered as logged-in user",
    "workbench.scope.empty": "No Chat available. Contact your admin or create one in Chat Management.",
    "workbench.generating": "Generating…",
    "workbench.noHit": "No matching content found. Try rephrasing or expanding the data scope.",
    "workbench.timeout": "Request timed out, please retry",
    "workbench.error": "Q&A failed, please retry",
    "workbench.error.429": "Too many requests, please try again later",
    "workbench.error.403": "Permission denied: your account cannot execute Q&A. Contact your admin.",
    "workbench.feedback.positive": "Marked as helpful",
    "workbench.feedback.negative": "Marked as needs improvement",
    "workbench.feedback.marked.positive": "Marked helpful",
    "workbench.feedback.marked.negative": "Marked needs improvement",
    "workbench.feedback.comment.placeholder": "Optional: enter improvement suggestion",
    "workbench.feedback.submit": "Submit",
    "workbench.copied": "Copied",
    "workbench.copyFailed": "Copy failed",
    "workbench.citation.view": "View source document",
    "workbench.input.placeholder": "Type a question, Enter to send (Shift+Enter for new line)",
    "workbench.cancel": "Cancel generation",
    "workbench.send": "Send",
    "workbench.retry": "Retry",
    "workbench.regenerate": "Regenerate",
  },
};

interface I18nState {
  language: Language;
  setLanguage: (l: Language) => void;
  t: (key: string) => string;
}

const I18nContext = createContext<I18nState | undefined>(undefined);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState<Language>(
    (localStorage.getItem("locale") as Language) || "zh",
  );

  const setLang = useCallback((l: Language) => {
    localStorage.setItem("locale", l);
    setLanguage(l);
  }, []);

  const t = useCallback(
    (key: string) => messages[language][key] ?? key,
    [language],
  );

  const value = useMemo<I18nState>(
    () => ({ language, setLanguage: setLang, t }),
    [language, setLang, t],
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nState {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used within I18nProvider");
  return ctx;
}
