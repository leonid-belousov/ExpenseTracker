import type { Category } from '../types';

// UI dictionaries. `ru` is the reference: its type defines the full set of
// keys (and the signatures of parameterised messages), so a missing or extra
// key in another language is a compile-time error.
//
// To add a language: add it to `LOCALES`, create a dictionary typed as
// `Messages`, and register it in `dictionaries`.

export const LOCALES = ['ru', 'en'] as const;
export type Locale = (typeof LOCALES)[number];
export const DEFAULT_LOCALE: Locale = 'ru';

export type ValidationCode =
  | 'memberRequired'
  | 'amountInvalid'
  | 'amountPositive'
  | 'categoryRequired'
  | 'dateRequired'
  | 'dateFuture'
  | 'commentTooLong';

export type ApiErrorKind =
  | 'network'
  | 'proxy'
  | 'validation'
  | 'notFound'
  | 'forbidden'
  | 'unavailable'
  | 'unknown';

const ru = {
  meta: {
    title: 'Семейные расходы',
    htmlLang: 'ru',
    intlLocale: 'ru-RU',
    // 15.10.2026
    dateOptions: { day: '2-digit', month: '2-digit', year: 'numeric' } as Intl.DateTimeFormatOptions,
  },
  header: {
    title: 'Семейные расходы',
    add: '+ Добавить трату',
    language: 'Язык интерфейса',
  },
  filters: {
    member: 'Член семьи',
    category: 'Категория',
    all: 'Все',
    from: 'С даты',
    to: 'По дату (включительно)',
    thisWeek: 'Эта неделя',
    thisMonth: 'Этот месяц',
    lastMonth: 'Прошлый месяц',
    allTime: 'Всё время',
    rangeError: 'Дата «по» не может быть раньше даты «с».',
  },
  list: {
    summary: (count: number, sum: string) => `Найдено: ${count} · Сумма: ${sum}`,
    loading: 'Загрузка…',
    empty: 'Трат по выбранным фильтрам нет.',
    loadFailed: (reason: string) => `Не удалось загрузить траты: ${reason}`,
    configFailed: (reason: string) => `Не удалось загрузить настройки: ${reason}`,
    prev: '← Назад',
    next: 'Вперёд →',
    page: (page: number, pages: number) => `Страница ${page} из ${pages}`,
  },
  table: {
    date: 'Дата',
    member: 'Член семьи',
    category: 'Категория',
    amount: 'Сумма',
    comment: 'Комментарий',
    actions: 'Действия',
    edit: 'Изменить',
    delete: 'Удалить',
  },
  dialog: {
    createTitle: 'Новая трата',
    editTitle: 'Изменить трату',
    member: 'Член семьи',
    amount: (currency: string) => `Сумма, ${currency}`,
    category: 'Категория',
    date: 'Дата траты',
    comment: 'Комментарий',
    choose: '— выберите —',
    cancel: 'Отмена',
    save: 'Сохранить',
    saving: 'Сохранение…',
    notSaved: (reason: string) => `Не сохранено: ${reason}`,
    alreadyDeleted: 'Трата уже удалена. Обновите список.',
  },
  remove: {
    confirm: (amount: string, category: string, date: string, member: string) =>
      `Удалить трату ${amount} (${category}, ${date}, ${member})?`,
    alreadyDeleted: 'Трата уже была удалена.',
    failed: (reason: string) => `Не удалось удалить трату: ${reason}`,
  },
  categories: {
    food: 'Еда',
    transport: 'Транспорт',
    housing: 'Жильё',
    entertainment: 'Развлечения',
    health: 'Здоровье',
    shopping: 'Покупки',
    other: 'Другое',
  } satisfies Record<Category, string>,
  validation: {
    memberRequired: 'Выберите члена семьи.',
    amountInvalid: 'Введите положительную сумму, не больше двух знаков после запятой.',
    amountPositive: 'Сумма должна быть больше нуля.',
    categoryRequired: 'Выберите категорию.',
    dateRequired: 'Укажите дату.',
    dateFuture: 'Дата не может быть в будущем.',
    commentTooLong: 'Комментарий не длиннее 500 символов.',
  } satisfies Record<ValidationCode, string>,
  // Shown next to a field the server rejected; the server's own text is English.
  serverField: {
    user_id: 'Сервер отклонил выбранного члена семьи.',
    amount: 'Сервер отклонил сумму: проверьте значение.',
    category: 'Сервер отклонил категорию.',
    spent_at: 'Сервер отклонил дату: она должна быть не раньше 2000 года и не в будущем.',
    comment: 'Сервер отклонил комментарий.',
  },
  apiErrors: {
    network: 'Сервер недоступен. Проверьте подключение и повторите попытку.',
    proxy: (status: number) => `Сервер API недоступен (${status}). Попробуйте позже.`,
    validation: 'Проверьте выделенные поля.',
    notFound: 'Трата не найдена.',
    forbidden: 'Запрос отклонён сервером (недопустимый источник).',
    unavailable: 'Хранилище недоступно, изменения не подтверждены. Попробуйте позже.',
    unknown: (status: number) => `Ошибка сервера (${status}).`,
    unexpected: 'непредвиденная ошибка',
  },
};

export type Messages = typeof ru;

const en: Messages = {
  meta: {
    title: 'Family expenses',
    htmlLang: 'en',
    intlLocale: 'en-US',
    // Oct 15, 2026 (unambiguous, unlike 10/15/2026)
    dateOptions: { month: 'short', day: 'numeric', year: 'numeric' },
  },
  header: {
    title: 'Family expenses',
    add: '+ Add expense',
    language: 'Interface language',
  },
  filters: {
    member: 'Family member',
    category: 'Category',
    all: 'All',
    from: 'From',
    to: 'To (inclusive)',
    thisWeek: 'This week',
    thisMonth: 'This month',
    lastMonth: 'Last month',
    allTime: 'All time',
    rangeError: 'The "to" date cannot be earlier than the "from" date.',
  },
  list: {
    summary: (count, sum) => `Found: ${count} · Total: ${sum}`,
    loading: 'Loading…',
    empty: 'No expenses match the selected filters.',
    loadFailed: (reason) => `Could not load expenses: ${reason}`,
    configFailed: (reason) => `Could not load settings: ${reason}`,
    prev: '← Previous',
    next: 'Next →',
    page: (page, pages) => `Page ${page} of ${pages}`,
  },
  table: {
    date: 'Date',
    member: 'Family member',
    category: 'Category',
    amount: 'Amount',
    comment: 'Comment',
    actions: 'Actions',
    edit: 'Edit',
    delete: 'Delete',
  },
  dialog: {
    createTitle: 'New expense',
    editTitle: 'Edit expense',
    member: 'Family member',
    amount: (currency) => `Amount, ${currency}`,
    category: 'Category',
    date: 'Expense date',
    comment: 'Comment',
    choose: '— select —',
    cancel: 'Cancel',
    save: 'Save',
    saving: 'Saving…',
    notSaved: (reason) => `Not saved: ${reason}`,
    alreadyDeleted: 'This expense has already been deleted. Refresh the list.',
  },
  remove: {
    confirm: (amount, category, date, member) => `Delete the expense ${amount} (${category}, ${date}, ${member})?`,
    alreadyDeleted: 'This expense had already been deleted.',
    failed: (reason) => `Could not delete the expense: ${reason}`,
  },
  categories: {
    food: 'Food',
    transport: 'Transport',
    housing: 'Housing',
    entertainment: 'Entertainment',
    health: 'Health',
    shopping: 'Shopping',
    other: 'Other',
  },
  validation: {
    memberRequired: 'Select a family member.',
    amountInvalid: 'Enter a positive amount with at most two decimal places.',
    amountPositive: 'The amount must be greater than zero.',
    categoryRequired: 'Select a category.',
    dateRequired: 'Enter a date.',
    dateFuture: 'The date cannot be in the future.',
    commentTooLong: 'The comment must be at most 500 characters.',
  },
  serverField: {
    user_id: 'The server rejected the selected family member.',
    amount: 'The server rejected the amount: check the value.',
    category: 'The server rejected the category.',
    spent_at: 'The server rejected the date: it must be in 2000 or later and not in the future.',
    comment: 'The server rejected the comment.',
  },
  apiErrors: {
    network: 'The server is unreachable. Check your connection and try again.',
    proxy: (status) => `The API server is unavailable (${status}). Please try again later.`,
    validation: 'Check the highlighted fields.',
    notFound: 'Expense not found.',
    forbidden: 'The server rejected the request (origin not allowed).',
    unavailable: 'Storage is unavailable; no changes were confirmed. Please try again later.',
    unknown: (status) => `Server error (${status}).`,
    unexpected: 'unexpected error',
  },
};

export const dictionaries: Record<Locale, Messages> = { ru, en };

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && (LOCALES as readonly string[]).includes(v);
}
