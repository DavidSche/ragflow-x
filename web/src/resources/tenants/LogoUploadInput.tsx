import { useInput } from "ra-core";
import { Label } from "@/components/ui/label";
import { LogoPickerRow } from "../../components/LogoPickerRow";

export function LogoUploadInput(props: { source: string; label?: string }) {
  const { field } = useInput(props);
  return (
    <div className="space-y-2">
      <Label>{props.label ?? "Logo"}</Label>
      <LogoPickerRow value={field.value ?? ""} onChange={field.onChange} />
    </div>
  );
}
