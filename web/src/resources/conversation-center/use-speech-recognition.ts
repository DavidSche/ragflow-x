import { useCallback, useEffect, useRef, useState } from "react";

type SpeechRecognitionResult = {
  isFinal: boolean;
  [index: number]: { transcript: string };
};

export type SpeechRecognitionLike = {
  lang: string;
  interimResults: boolean;
  continuous: boolean;
  onresult: ((event: { results: ArrayLike<SpeechRecognitionResult> }) => void) | null;
  onend: (() => void) | null;
  onerror: (() => void) | null;
  start: () => void;
  stop: () => void;
};

type SpeechRecognitionWindow = Window & {
  SpeechRecognition?: new () => SpeechRecognitionLike;
  webkitSpeechRecognition?: new () => SpeechRecognitionLike;
};

function speechRecognitionCtor(): (new () => SpeechRecognitionLike) | undefined {
  if (typeof window === "undefined") return undefined;
  const speechWindow = window as SpeechRecognitionWindow;
  return speechWindow.SpeechRecognition ?? speechWindow.webkitSpeechRecognition;
}

export function useSpeechRecognition({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const [listening, setListening] = useState(false);
  const [supported] = useState(() => Boolean(speechRecognitionCtor()));
  const valueRef = useRef(value);
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);

  useEffect(() => {
    valueRef.current = value;
  }, [value]);

  useEffect(() => () => {
    recognitionRef.current?.stop();
    recognitionRef.current = null;
    setListening(false);
  }, []);

  const toggle = useCallback(() => {
    const Recognition = speechRecognitionCtor();
    if (!Recognition) return;
    if (listening) {
      recognitionRef.current?.stop();
      return;
    }

    const recognition = new Recognition();
    recognition.lang = "zh-CN";
    recognition.interimResults = true;
    recognition.continuous = false;
    let finalText = "";
    recognition.onresult = (event) => {
      let interimText = "";
      for (let index = 0; index < event.results.length; index += 1) {
        const result = event.results[index];
        const transcript = result[0]?.transcript ?? "";
        if (result.isFinal) finalText += transcript;
        else interimText += `${transcript} `;
      }
      const base = valueRef.current.replace(/\s+$/, "");
      onChange(`${base ? `${base} ` : ""}${finalText}${interimText ? ` ${interimText}` : ""}`.trim());
    };
    recognition.onend = () => setListening(false);
    recognition.onerror = () => setListening(false);
    recognitionRef.current = recognition;
    setListening(true);
    try {
      recognition.start();
    } catch {
      recognitionRef.current = null;
      setListening(false);
    }
  }, [listening, onChange]);

  return { listening, supported, toggle };
}
