"use client";

import * as React from "react";
import { useEffect, useRef, useState } from "react";
import { DialogContent } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";

interface ResizableDialogContentProps
  extends React.ComponentProps<typeof DialogContent> {
  defaultWidth?: number;
  defaultHeight?: number;
}

export const ResizableDialogContent = ({
  children,
  className,
  style,
  defaultWidth = 1000,
  defaultHeight = 620,
  ...props
}: ResizableDialogContentProps) => {
  const [size, setSize] = useState({ width: defaultWidth, height: defaultHeight });
  const [resizing, setResizing] = useState(false);
  const start = useRef<{ x: number; y: number; w: number; h: number } | null>(null);

  useEffect(() => {
    if (!resizing) return;
    const move = (e: PointerEvent) => {
      if (!start.current) return;
      const w = Math.max(560, start.current.w + (e.clientX - start.current.x));
      const h = Math.max(400, start.current.h + (e.clientY - start.current.y));
      setSize({
        width: Math.min(w, window.innerWidth - 32),
        height: Math.min(h, window.innerHeight - 32),
      });
    };
    const up = () => {
      setResizing(false);
      start.current = null;
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    return () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
  }, [resizing]);

  const onHandleDown = (e: React.PointerEvent) => {
    e.preventDefault();
    e.stopPropagation();
    start.current = { x: e.clientX, y: e.clientY, w: size.width, h: size.height };
    setResizing(true);
  };

  return (
    <DialogContent
      className={cn(
        "flex max-h-[calc(100vh-2rem)] max-w-[calc(100vw-2rem)] flex-col overflow-hidden p-4",
        className,
      )}
      style={{ width: size.width, height: size.height, maxWidth: "calc(100vw - 2rem)", maxHeight: "calc(100vh - 2rem)", ...style }}
      {...props}
    >
      <div className="flex h-full min-h-0 w-full flex-col overflow-hidden">{children}</div>
      <div
        className="text-muted-foreground absolute right-1 bottom-1 z-10 h-4 w-4 cursor-nwse-resize select-none"
        onPointerDown={onHandleDown}
        aria-hidden
        title="拖动调整大小"
      >
        <svg viewBox="0 0 16 16" className="h-full w-full">
          <path
            d="M14 10v4h-4M14 6v2M12 14H10"
            stroke="currentColor"
            strokeWidth="1.5"
            fill="none"
            strokeLinecap="round"
          />
        </svg>
      </div>
    </DialogContent>
  );
};
