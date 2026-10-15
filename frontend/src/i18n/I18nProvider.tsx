import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { formatDate, formatMoney } from '../format';
import { DEFAULT_LOCALE, dictionaries, isLocale, type Locale, type Messages } from './messages';

const STORAGE_KEY = 'expense-tracker.locale';

/** Reads the saved language. Storage may be unavailable (private mode,
 * blocked cookies); the default language is used then. */
export function loadLocale(storage: Pick<Storage, 'getItem'> | undefined = safeStorage()): Locale {
  try {
    const saved = storage?.getItem(STORAGE_KEY);
    return isLocale(saved) ? saved : DEFAULT_LOCALE;
  } catch {
    return DEFAULT_LOCALE;
  }
}

function saveLocale(locale: Locale) {
  try {
    safeStorage()?.setItem(STORAGE_KEY, locale);
  } catch {
    /* the choice just won't persist */
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof window === 'undefined' ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

export interface I18n {
  locale: Locale;
  setLocale: (l: Locale) => void;
  m: Messages;
  money: (amount: string, currency: string) => string;
  date: (isoDate: string) => string;
}

const I18nContext = createContext<I18n | null>(null);

export function I18nProvider({ children, initialLocale }: { children: ReactNode; initialLocale?: Locale }) {
  const [locale, setLocaleState] = useState<Locale>(() => initialLocale ?? loadLocale());

  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    saveLocale(l);
  }, []);

  const value = useMemo<I18n>(() => {
    const m = dictionaries[locale];
    return {
      locale,
      setLocale,
      m,
      money: (amount, currency) => formatMoney(amount, currency, locale),
      date: (iso) => formatDate(iso, m.meta.intlLocale, m.meta.dateOptions),
    };
  }, [locale, setLocale]);

  useEffect(() => {
    document.documentElement.lang = value.m.meta.htmlLang;
    document.title = value.m.meta.title;
  }, [value]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18n {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error('useI18n must be used inside <I18nProvider>');
  return ctx;
}
