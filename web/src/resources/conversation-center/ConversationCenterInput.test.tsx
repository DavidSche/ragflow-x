import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SpeechRecognitionLike } from "./use-speech-recognition";
import { ConversationCenterInput } from "./ConversationCenterInput";

const baseProps = {
  input: "",
  inputRef: { current: null },
  attachments: [],
  paletteOpen: false,
  paletteId: "palette",
  activeOptionIndex: 0,
  activeOptionId: undefined,
  commandOptions: [],
  targetOptions: [],
  disabled: false,
  busy: false,
  onChange: vi.fn(),
  onKeyDown: vi.fn(),
  onCompositionStart: vi.fn(),
  onCompositionEnd: vi.fn(),
  onRemoveAttachment: vi.fn(),
  onRunCommand: vi.fn(),
  onChooseTarget: vi.fn(),
  onCancel: vi.fn(),
  onSubmit: vi.fn(),
};

describe("ConversationCenterInput voice input", () => {
  afterEach(() => {
    Reflect.deleteProperty(window, "SpeechRecognition");
  });

  it("hides voice input when the browser capability is unavailable", () => {
    render(<ConversationCenterInput {...baseProps} />);

    expect(screen.queryByRole("button", { name: "conversationCenter.voice_input" })).not.toBeInTheDocument();
  });

  it("uses browser speech recognition and appends the transcript", () => {
    const recognitions: SpeechRecognitionLike[] = [];
    class MockSpeechRecognition implements SpeechRecognitionLike {
      lang = "zh-CN";
      interimResults = true;
      continuous = false;
      onresult: SpeechRecognitionLike["onresult"] = null;
      onend = null;
      onerror = null;
      start() {}
      stop() {}
      constructor() {
        recognitions.push(this);
      }
    }
    Object.defineProperty(window, "SpeechRecognition", {
      configurable: true,
      value: MockSpeechRecognition,
    });

    render(<ConversationCenterInput {...baseProps} />);
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.voice_input" }));
    const recognition = recognitions[0];
    recognition.onresult?.({ results: [{ isFinal: true, 0: { transcript: "hello" } }] });

    expect(screen.getByRole("button", { name: "conversationCenter.stop_voice_input" })).toBeInTheDocument();
    expect(baseProps.onChange).toHaveBeenCalledWith("hello");
  });
});
