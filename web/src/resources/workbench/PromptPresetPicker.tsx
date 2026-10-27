import { cn } from "@/lib/utils";
import {
  PARAMETER_PROFILES,
  PROMPT_PRESETS,
  type ParameterProfileId,
  type PromptPresetId,
} from "./prompt-presets";

interface PromptPresetPickerProps {
  activePresetId?: PromptPresetId;
  activeProfileId?: ParameterProfileId;
  onSelectPreset: (presetId: PromptPresetId) => void;
  onSelectProfile: (profileId: ParameterProfileId) => void;
  showPrompts?: boolean;
  className?: string;
}

const PresetCard = ({
  label,
  description,
  active,
  onClick,
}: {
  label: string;
  description: string;
  active: boolean;
  onClick: () => void;
}) => (
  <button
    type="button"
    onClick={onClick}
    aria-pressed={active}
    className={cn(
      "rounded border p-3 text-left transition-colors",
      active ? "border-primary bg-primary/5" : "bg-background hover:border-primary/60 hover:bg-muted/40",
    )}
  >
    <div className="text-sm font-medium">{label}</div>
    <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">{description}</p>
  </button>
);

export function PromptPresetPicker({
  activePresetId,
  activeProfileId,
  onSelectPreset,
  onSelectProfile,
  showPrompts = true,
  className,
}: PromptPresetPickerProps) {
  return (
    <div className={cn("space-y-3 rounded border p-3", className)}>
      {showPrompts ? (
        <section aria-label="提示词模板">
          <h4 className="text-sm font-medium">提示词模板</h4>
          <p className="mt-1 text-xs text-muted-foreground">
            按业务场景一键填充系统提示词、开场白和无答案话术。
          </p>
          <div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
            {PROMPT_PRESETS.map((preset) => (
              <PresetCard
                key={preset.id}
                label={preset.label}
                description={preset.description}
                active={activePresetId === preset.id}
                onClick={() => onSelectPreset(preset.id)}
              />
            ))}
          </div>
        </section>
      ) : null}
      <section aria-label="参数档位">
        <h4 className="text-sm font-medium">参数档位</h4>
        <p className="mt-1 text-xs text-muted-foreground">
          选择标准参数组合，仍可在下方按应用继续微调。
        </p>
        <div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-5">
          {PARAMETER_PROFILES.map((profile) => (
            <PresetCard
              key={profile.id}
              label={profile.label}
              description={profile.description}
              active={activeProfileId === profile.id}
              onClick={() => onSelectProfile(profile.id)}
            />
          ))}
        </div>
      </section>
    </div>
  );
}
