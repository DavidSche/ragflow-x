import polyglotI18nProvider from "ra-i18n-polyglot";
import enMessages from "./i18n/en";
import zhMessages from "./i18n/zh";
import type { TranslationMessages } from "ra-core";


export const i18nProvider = polyglotI18nProvider(
  (locale) =>
    (locale === "zh" ? zhMessages : enMessages) as unknown as TranslationMessages,
  "zh",
  [
    { locale: "zh", name: "中文" },
    { locale: "en", name: "English" },
  ],
  { allowMissing: true },
);
