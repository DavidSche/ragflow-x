import { createContext } from "react";

export type Theme = "dark" | "dark-blue" | "dark-green" | "light" | "light-blue" | "light-green" | "system";

export type ThemeProviderState = {
  theme: Theme;
  setTheme: (theme: Theme) => void;
};

const initialState: ThemeProviderState = {
  theme: "system",
  setTheme: () => null,
};

export const ThemeProviderContext =
  createContext<ThemeProviderState>(initialState);
