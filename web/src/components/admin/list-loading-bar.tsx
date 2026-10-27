import { useListContext } from "ra-core";
import { cn } from "@/lib/utils";

/**
 * A thin animated loading bar that appears at the top of a list page
 * while data is being fetched (initial load, filter change, pagination).
 *
 * Uses the list context's `isLoading` state to show/hide.
 */
export const ListLoadingBar = ({ className }: { className?: string }) => {
  const { isLoading } = useListContext();

  return (
    <div
      className={cn(
        "relative h-0.5 w-full overflow-hidden rounded-full bg-primary/10",
        className,
      )}
    >
      <div
        className={cn(
          "absolute inset-y-0 left-0 bg-primary transition-all duration-300",
          isLoading ? "w-full animate-loading-bar" : "w-0",
        )}
      />
    </div>
  );
};
