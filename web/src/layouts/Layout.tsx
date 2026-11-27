import type { ErrorInfo } from "react";
import { Suspense, useEffect, useState } from "react";
import type { CoreLayoutProps } from "ra-core";
import { ErrorBoundary } from "react-error-boundary";
import { cn } from "@/lib/utils";
import { SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { UserMenu } from "@/components/admin/user-menu";
import { ThemeModeToggle } from "@/components/admin/theme-mode-toggle";
import { Notification } from "@/components/admin/notification";
import { RefreshButton } from "@/components/admin/refresh-button";
import { LocalesMenuButton } from "@/components/admin/locales-menu-button";
import { Error } from "@/components/admin/error";
import { Loading } from "@/components/admin/loading";
import { AppSidebar } from "./AppSidebar";
import { DEFAULT_BRAND_NAME, loadBranding, useBranding } from "../lib/branding";
import { useVisualViewportHeight } from "../lib/visualViewport";

export const Layout = (props: CoreLayoutProps) => {
  const [errorInfo, setErrorInfo] = useState<ErrorInfo | undefined>(undefined);
  const brandName = useBranding().name || DEFAULT_BRAND_NAME;
  // doc/314 第四节末：壳的高度跟着 visualViewport 走，软键盘弹起时输入框才不会被
  // 盖住。变量写在 documentElement 上，这里只负责订阅；没有 `visualViewport` 的
  // 环境（桌面 Safari、jsdom）由 `100dvh` 兜底。为什么订阅在壳上而不是输入框上：
  // 视觉视口只有一个，键盘把壳压矮之后，壳里每一个 flex 列都跟着矮。
  useVisualViewportHeight();
  // The browser tab follows the configured system name, like the sidebar and
  // login page do; falls back to the default until branding resolves.
  useEffect(() => {
    void loadBranding();
  }, []);
  useEffect(() => {
    document.title = brandName;
  }, [brandName]);
  const handleError = (_: unknown, info: ErrorInfo) => {
    setErrorInfo(info);
  };
  return (
    <SidebarProvider>
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:left-2 focus:z-[9999] focus:rounded-md focus:bg-primary focus:px-4 focus:py-2 focus:text-primary-foreground focus:shadow-lg"
      >
        跳转到主内容
      </a>
      <AppSidebar />
      <main id="main-content" aria-label="主内容区域"
        className={cn(
          "ml-auto w-full max-w-full",
          "peer-data-[state=collapsed]:w-[calc(100%-var(--sidebar-width-icon)-1rem)]",
          "peer-data-[state=expanded]:w-[calc(100%-var(--sidebar-width))]",
          "sm:transition-[width] sm:duration-200 sm:ease-linear",
          "flex h-[var(--rgx-viewport-height,100dvh)] flex-col",
          "group-data-[scroll-locked=1]/body:h-full",
          "has-[main.fixed-main]:group-data-[scroll-locked=1]/body:h-[var(--rgx-viewport-height,100dvh)]",
        )}
      >
        <header className="flex h-16 md:h-12 shrink-0 items-center gap-2 px-4" aria-label="顶部工具栏">
          {/* 44px 是拇指的尺寸，不是设计的尺寸：`size-7` 在手机上点不中
              （doc/314 第四节末）。`md:` 之后回到接近原样的 32px。 */}
          <SidebarTrigger className="size-11 md:size-8" />
          <div className="flex-1 flex items-center" id="breadcrumb" />
          <LocalesMenuButton />
          <ThemeModeToggle />
          <RefreshButton />
          <UserMenu />
        </header>
        <ErrorBoundary
          onError={handleError}
          fallbackRender={({ error, resetErrorBoundary }) => (
            <Error
              error={error}
              errorInfo={errorInfo}
              resetErrorBoundary={resetErrorBoundary}
            />
          )}
        >
          <Suspense fallback={<Loading />}>
            <div className="flex min-h-0 flex-1 flex-col px-4">{props.children}</div>
          </Suspense>
        </ErrorBoundary>
      </main>
      <Notification />
    </SidebarProvider>
  );
};
