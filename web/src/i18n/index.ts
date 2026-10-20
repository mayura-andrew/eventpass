// SPDX-License-Identifier: Apache-2.0

import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./en.json";

// Contributors add a language by copying en.json to <code>.json and listing
// it here (see CONTRIBUTING.md). Right-to-left codes set dir="rtl".
const RTL = new Set(["ar", "he", "fa", "ur"]);

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en } },
  lng: "en",
  fallbackLng: "en",
  interpolation: { escapeValue: false }, // React escapes
  returnNull: false,
});

i18n.on("languageChanged", (lng) => {
  document.documentElement.lang = lng;
  document.documentElement.dir = RTL.has(lng.split("-")[0] ?? "") ? "rtl" : "ltr";
});

export default i18n;
