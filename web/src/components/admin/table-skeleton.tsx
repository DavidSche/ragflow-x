import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

/**
 * Animated skeleton placeholder for DataTable during loading.
 *
 * Renders a table with shimmer rows matching a typical data table layout.
 * The number of rows and columns can be customized.
 */
export const TableSkeleton = ({
  rows = 5,
  columns = 4,
}: TableSkeletonProps) => {
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            {Array.from({ length: columns }).map((_, i) => (
              <TableHead key={i}>
                <Skeleton className="h-4 w-20" />
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {Array.from({ length: rows }).map((_, rowIndex) => (
            <TableRow key={rowIndex}>
              {Array.from({ length: columns }).map((_, colIndex) => (
                <TableCell key={colIndex}>
                  <Skeleton
                    className="h-4"
                    style={{
                      width: `${60 + Math.sin(rowIndex * 3 + colIndex * 7) * 20}%`,
                    }}
                  />
                </TableCell>
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
};

export interface TableSkeletonProps {
  /** Number of skeleton rows to render (default: 5) */
  rows?: number;
  /** Number of skeleton columns to render (default: 4) */
  columns?: number;
}
