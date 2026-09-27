import { toast } from "sonner";

import { ApiError } from "./api";

export function optionalWarning(error: unknown, message: string): void {
  const detail = error instanceof ApiError ? error.displayMessage : undefined;
  toast.error(message, detail ? { description: detail } : undefined);
}
