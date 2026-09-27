import { useState, type ReactNode } from "react";
import { CreateBase, useNotify } from "ra-core";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { SaveButton, SimpleForm } from "@/components/admin";
import { ApiError } from "../lib/api";

export function CreateDialog({
  resource,
  title = "新建",
  children,
}: {
  resource: string;
  title?: string;
  children: ReactNode;
}) {
  const notify = useNotify();
  const [open, setOpen] = useState(false);

  const toolbar = (
    <div className="flex justify-end gap-2 pt-2">
      <Button type="button" variant="outline" onClick={() => setOpen(false)}>
        取消
      </Button>
      <SaveButton />
    </div>
  );

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button onClick={() => setOpen(true)}>
          <Plus /> {title}
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-lg">
        <DialogHeader className="shrink-0">
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="flex-1 overflow-y-auto pr-1">
          <CreateBase
            resource={resource}
            redirect={false}
            mutationOptions={{
              onSuccess: () => {
                notify("已创建", { type: "success" });
                setOpen(false);
              },
              onError: (error) => {
                notify(
                  error instanceof ApiError
                    ? error.displayMessage
                    : error instanceof Error
                      ? error.message
                    : "创建失败，请检查输入后重试",
                  { type: "error" },
                );
              },
            }}
          >
            <SimpleForm toolbar={toolbar}>{children}</SimpleForm>
          </CreateBase>
        </div>
      </DialogContent>
    </Dialog>
  );
}
