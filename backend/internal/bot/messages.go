package bot

import (
	"fmt"

	"expensetracker/internal/agent"
	"expensetracker/internal/expense"
)

// Messages is the catalogue of the bot's fixed texts for one language. The
// agent writes the conversational part of replies itself; these cover access
// control, failures and the receipt of changes. Add a language by adding a
// catalogue to catalogs; canonical categories never change.
type Messages struct {
	DateLayout     string
	Categories     map[expense.Category]string
	Help           string
	NotAllowed     string // %d = user's own Telegram id
	PrivateOnly    string
	TextOnly       string
	LLMUnavailable string
	LLMMalformed   string
	StepLimit      string
	StorageFailed  string
	Created        string // %s = expense line
	Updated        string // %s = old, %s = new
	Deleted        string // %s = expense line
}

var catalogs = map[string]Messages{
	"ru": {
		DateLayout: "02.01.2006",
		Categories: map[expense.Category]string{
			expense.Food: "еда", expense.Transport: "транспорт", expense.Housing: "жильё",
			expense.Entertainment: "развлечения", expense.Health: "здоровье", expense.Shopping: "покупки", expense.Other: "другое",
		},
		Help: "Я записываю ваши траты. Примеры:\n" +
			"• «кофе 350» или «такси 1200 вчера» — добавить (можно несколько: «кофе 350, булка 120»)\n" +
			"• «сколько потратил на еду в этом месяце» — сумма\n" +
			"• «покажи траты за неделю» — список\n" +
			"• «последнее было не 350, а 450» — исправить\n" +
			"• «удали такси» — удалить\n" +
			"Я помню последние сообщения, так что на мои уточняющие вопросы можно отвечать коротко.\n" +
			"Вижу и меняю только ваши собственные траты.",
		NotAllowed:     "Доступ закрыт. Ваш Telegram ID: %d — попросите главу семьи добавить его в настройки бота.",
		PrivateOnly:    "Я работаю только в личных сообщениях.",
		TextOnly:       "Пожалуйста, напишите текстом, например: «кофе 350».",
		LLMUnavailable: "Не получилось обработать сообщение: сервис распознавания сейчас недоступен. Ничего не изменено, попробуйте ещё раз позже.",
		LLMMalformed:   "Не получилось понять сообщение. Ничего не изменено. Попробуйте сформулировать иначе, например: «такси 1200 вчера».",
		StepLimit:      "Запрос оказался слишком сложным, я не довёл его до конца. Ничего не изменено — попробуйте разбить его на части.",
		StorageFailed:  "Не удалось выполнить операцию: хранилище недоступно. Ничего не изменено, попробуйте позже.",
		Created:        "Записал: %s",
		Updated:        "Исправил: %s → %s",
		Deleted:        "Удалил: %s",
	},
	"en": {
		DateLayout: "2006-01-02",
		Categories: map[expense.Category]string{
			expense.Food: "food", expense.Transport: "transport", expense.Housing: "housing",
			expense.Entertainment: "entertainment", expense.Health: "health", expense.Shopping: "shopping", expense.Other: "other",
		},
		Help: "I record your expenses. Examples:\n" +
			"• \"coffee 350\" or \"taxi 1200 yesterday\" — add (several at once: \"coffee 350, bun 120\")\n" +
			"• \"how much did I spend on food this month\" — total\n" +
			"• \"show expenses for the week\" — list\n" +
			"• \"the last one was 450, not 350\" — correct\n" +
			"• \"delete the taxi\" — delete\n" +
			"I remember recent messages, so you can answer my questions briefly.\n" +
			"I only see and change your own expenses.",
		NotAllowed:     "Access denied. Your Telegram ID is %d — ask the head of the family to add it to the bot settings.",
		PrivateOnly:    "I only work in private chats.",
		TextOnly:       "Please send text, e.g. \"coffee 350\".",
		LLMUnavailable: "I couldn't process the message: the language service is unavailable. Nothing was changed; please try again later.",
		LLMMalformed:   "I couldn't understand the message. Nothing was changed. Try rephrasing, e.g. \"taxi 1200 yesterday\".",
		StepLimit:      "The request turned out too complex and I didn't finish it. Nothing was changed — try splitting it up.",
		StorageFailed:  "The operation failed: storage is unavailable. Nothing was changed; please try again later.",
		Created:        "Saved: %s",
		Updated:        "Updated: %s → %s",
		Deleted:        "Deleted: %s",
	},
}

// Catalog returns the catalogue for lang, falling back to Russian.
func Catalog(lang string) Messages {
	if m, ok := catalogs[lang]; ok {
		return m
	}
	return catalogs["ru"]
}

// expenseLine renders "15.10.2026 · 350 RUB · еда · кофе".
func (m Messages) expenseLine(e expense.Expense, currency string) string {
	s := fmt.Sprintf("%s · %s %s · %s", e.SpentAt.Format(m.DateLayout), e.Amount.Human(), currency, m.Categories[e.Category])
	if e.Comment != nil {
		s += " · " + *e.Comment
	}
	return s
}

// receipt renders one line per change, generated from what the tools
// actually did (never from the model's text).
func (m Messages) receipt(changes []agent.Change, currency string) []string {
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		switch c.Kind {
		case agent.Created:
			lines = append(lines, fmt.Sprintf(m.Created, m.expenseLine(c.After, currency)))
		case agent.Updated:
			lines = append(lines, fmt.Sprintf(m.Updated, m.expenseLine(c.Before, currency), m.expenseLine(c.After, currency)))
		case agent.Deleted:
			lines = append(lines, fmt.Sprintf(m.Deleted, m.expenseLine(c.Before, currency)))
		}
	}
	return lines
}
