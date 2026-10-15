package bot

import (
	"fmt"
	"strings"

	"expensetracker/internal/dates"
	"expensetracker/internal/expense"
)

// Messages is a reply catalogue for one language. Add a language by adding a
// new catalogue to catalogs; canonical categories never change.
type Messages struct {
	DateLayout     string
	Categories     map[expense.Category]string
	Help           string
	NotAllowed     string // %d = user's own Telegram id
	PrivateOnly    string
	TextOnly       string
	LLMUnavailable string
	LLMMalformed   string
	StorageFailed  string
	AskAmount      string
	AskCategory    string // %s = category list
	AskWhatChange  string
	AskWhichOne    string
	NotFound       string
	Ambiguous      string // %d = count
	AndMore        string // %d = remaining
	LastMismatch   string // %s = last expense
	Created        string // %s = expense line
	Updated        string // %s = old, %s = new
	Deleted        string // %s = expense line
	ListHeader     string // %s = scope description
	ListEmpty      string // %s = scope description
	ListFooter     string // %d count, %s sum, %s currency
	ListTruncated  string // %d shown
	Summary        string // %s scope, %s sum, %s currency, %d count
	PeriodRange    string // %s from, %s to
	PeriodDay      string // %s day
	InCategory     string // %s category
	InvalidAmount  string
	InvalidDate    string
	InvalidComment string
	InvalidPeriod  string
	Default        string // generic clarification
}

var catalogs = map[string]Messages{
	"ru": {
		DateLayout: "02.01.2006",
		Categories: map[expense.Category]string{
			expense.Food: "еда", expense.Transport: "транспорт", expense.Housing: "жильё",
			expense.Entertainment: "развлечения", expense.Health: "здоровье", expense.Shopping: "покупки", expense.Other: "другое",
		},
		Help: "Я записываю ваши траты. Примеры:\n" +
			"• «кофе 350» или «такси 1200 вчера» — добавить\n" +
			"• «сколько потратил на еду в этом месяце» — сумма\n" +
			"• «покажи траты за неделю» — список\n" +
			"• «последнее было не 350, а 450» — исправить\n" +
			"• «удали такси» — удалить\n" +
			"Вижу и меняю только ваши собственные траты.",
		NotAllowed:     "Доступ закрыт. Ваш Telegram ID: %d — попросите главу семьи добавить его в настройки бота.",
		PrivateOnly:    "Я работаю только в личных сообщениях.",
		TextOnly:       "Пожалуйста, напишите текстом, например: «кофе 350».",
		LLMUnavailable: "Не получилось разобрать сообщение: сервис распознавания сейчас недоступен. Ничего не изменено, попробуйте ещё раз позже.",
		LLMMalformed:   "Не получилось понять сообщение. Ничего не изменено. Попробуйте сформулировать иначе, например: «такси 1200 вчера».",
		StorageFailed:  "Не удалось выполнить операцию: хранилище недоступно. Ничего не изменено, попробуйте позже.",
		AskAmount:      "Какая сумма? Например: «кофе 350».",
		AskCategory:    "К какой категории отнести трату? Повторите сообщение с категорией: %s. Ничего не сохранено.",
		AskWhatChange:  "Что нужно изменить: сумму, категорию, дату или комментарий? Ничего не изменено.",
		AskWhichOne:    "Какую именно трату? Уточните сумму, дату или описание. Ничего не изменено.",
		NotFound:       "Не нашёл такой траты среди ваших. Ничего не изменено.",
		Ambiguous:      "Нашёл несколько подходящих трат (%d). Ничего не изменено — уточните сумму, дату или описание:",
		AndMore:        "…и ещё %d",
		LastMismatch:   "Последняя ваша трата — %s. Она не совпадает с запросом, поэтому ничего не изменено. Уточните, какую трату имеете в виду.",
		Created:        "Записал: %s",
		Updated:        "Исправил: %s → %s",
		Deleted:        "Удалил: %s",
		ListHeader:     "Траты %s:",
		ListEmpty:      "Трат %s нет.",
		ListFooter:     "Итого: %d шт. на %s %s",
		ListTruncated:  "Показаны последние %d.",
		Summary:        "Потрачено %s: %s %s (%d шт.)",
		PeriodRange:    "за %s – %s",
		PeriodDay:      "за %s",
		InCategory:     "в категории «%s»",
		InvalidAmount:  "Сумма должна быть больше нуля и иметь не больше двух знаков после запятой. Ничего не изменено.",
		InvalidDate:    "Дата не может быть в будущем или раньше 2000 года. Ничего не изменено.",
		InvalidComment: "Комментарий слишком длинный (максимум 500 символов). Ничего не изменено.",
		InvalidPeriod:  "Не понял период: конец раньше начала. Ничего не изменено.",
		Default:        "Уточните, пожалуйста, что нужно сделать. Ничего не изменено.",
	},
	"en": {
		DateLayout: "2006-01-02",
		Categories: map[expense.Category]string{
			expense.Food: "food", expense.Transport: "transport", expense.Housing: "housing",
			expense.Entertainment: "entertainment", expense.Health: "health", expense.Shopping: "shopping", expense.Other: "other",
		},
		Help: "I record your expenses. Examples:\n" +
			"• \"coffee 350\" or \"taxi 1200 yesterday\" — add\n" +
			"• \"how much did I spend on food this month\" — total\n" +
			"• \"show expenses for the week\" — list\n" +
			"• \"the last one was 450, not 350\" — correct\n" +
			"• \"delete the taxi\" — delete\n" +
			"I only see and change your own expenses.",
		NotAllowed:     "Access denied. Your Telegram ID is %d — ask the head of the family to add it to the bot settings.",
		PrivateOnly:    "I only work in private chats.",
		TextOnly:       "Please send text, e.g. \"coffee 350\".",
		LLMUnavailable: "I couldn't process the message: the language service is unavailable. Nothing was changed; please try again later.",
		LLMMalformed:   "I couldn't understand the message. Nothing was changed. Try rephrasing, e.g. \"taxi 1200 yesterday\".",
		StorageFailed:  "The operation failed: storage is unavailable. Nothing was changed; please try again later.",
		AskAmount:      "What was the amount? For example: \"coffee 350\".",
		AskCategory:    "Which category is it? Please resend with one of: %s. Nothing was saved.",
		AskWhatChange:  "What should I change: amount, category, date or comment? Nothing was changed.",
		AskWhichOne:    "Which expense do you mean? Add the amount, date or description. Nothing was changed.",
		NotFound:       "I couldn't find such an expense among yours. Nothing was changed.",
		Ambiguous:      "Several expenses match (%d). Nothing was changed — please add the amount, date or description:",
		AndMore:        "…and %d more",
		LastMismatch:   "Your most recent expense is %s. It doesn't match the request, so nothing was changed. Please specify which expense you mean.",
		Created:        "Saved: %s",
		Updated:        "Updated: %s → %s",
		Deleted:        "Deleted: %s",
		ListHeader:     "Expenses %s:",
		ListEmpty:      "No expenses %s.",
		ListFooter:     "Total: %d items, %s %s",
		ListTruncated:  "Showing the latest %d.",
		Summary:        "Spent %s: %s %s (%d items)",
		PeriodRange:    "for %s – %s",
		PeriodDay:      "for %s",
		InCategory:     "in \"%s\"",
		InvalidAmount:  "The amount must be greater than zero with at most two decimal places. Nothing was changed.",
		InvalidDate:    "The date can't be in the future or before 2000. Nothing was changed.",
		InvalidComment: "The comment is too long (500 characters max). Nothing was changed.",
		InvalidPeriod:  "I couldn't understand the period: it ends before it starts. Nothing was changed.",
		Default:        "Please clarify what you want to do. Nothing was changed.",
	},
}

// Catalog returns the catalogue for lang, falling back to Russian.
func Catalog(lang string) Messages {
	if m, ok := catalogs[lang]; ok {
		return m
	}
	return catalogs["ru"]
}

func (m Messages) date(d dates.Date) string { return d.Format(m.DateLayout) }

func (m Messages) categoryList() string {
	parts := make([]string, len(expense.Categories))
	for i, c := range expense.Categories {
		parts[i] = m.Categories[c]
	}
	return strings.Join(parts, ", ")
}

func (m Messages) period(r dates.Range) string {
	last := r.LastDay()
	if r.From.Equal(last) {
		return fmt.Sprintf(m.PeriodDay, m.date(r.From))
	}
	return fmt.Sprintf(m.PeriodRange, m.date(r.From), m.date(last))
}

// expenseLine renders "15.10.2026 · 350 RUB · еда · кофе".
func (m Messages) expenseLine(e expense.Expense, currency string) string {
	s := fmt.Sprintf("%s · %s %s · %s", m.date(e.SpentAt), e.Amount.Human(), currency, m.Categories[e.Category])
	if e.Comment != nil {
		s += " · " + *e.Comment
	}
	return s
}
