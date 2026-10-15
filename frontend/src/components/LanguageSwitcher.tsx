import { useI18n } from '../i18n/I18nProvider';
import { LOCALES, type Locale } from '../i18n/messages';

const LABELS: Record<Locale, string> = { ru: 'RU', en: 'EN' };
const NAMES: Record<Locale, string> = { ru: 'Русский', en: 'English' };

export function LanguageSwitcher() {
  const { locale, setLocale, m } = useI18n();
  return (
    <div className="lang-switch" role="group" aria-label={m.header.language}>
      {LOCALES.map((l) => (
        <button
          key={l}
          type="button"
          className={l === locale ? 'btn small active' : 'btn small'}
          aria-pressed={l === locale}
          lang={l}
          title={NAMES[l]}
          onClick={() => setLocale(l)}
        >
          {LABELS[l]}
        </button>
      ))}
    </div>
  );
}
