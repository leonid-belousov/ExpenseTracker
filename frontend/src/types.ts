// Shapes of the Go REST API (see backend/internal/api). Amounts are decimal
// strings ("350.50") and dates are calendar dates ("2026-10-15").

export type Category =
  | 'food'
  | 'transport'
  | 'housing'
  | 'entertainment'
  | 'health'
  | 'shopping'
  | 'other';

export interface Member {
  id: number;
  name: string;
}

export interface AppConfig {
  currency: string;
  timezone: string;
  today: string;
  categories: Category[];
  members: Member[];
  max_limit: number;
}

export interface Expense {
  id: number;
  user_id: number;
  member_name: string;
  amount: string;
  category: Category;
  spent_at: string;
  comment: string | null;
  created_at: string;
  updated_at: string;
}

export interface ExpensePage {
  items: Expense[];
  total: number;
  sum: string;
  currency: string;
  limit: number;
  offset: number;
}

export interface ExpenseInput {
  user_id: number;
  amount: string;
  category: Category;
  spent_at: string;
  comment: string | null;
}

/** Dashboard filter state. `to` is the inclusive end date shown in the UI. */
export interface Filters {
  userId: string;
  category: string;
  from: string;
  to: string;
}

export type FieldErrors = Partial<Record<keyof ExpenseInput, string>>;
