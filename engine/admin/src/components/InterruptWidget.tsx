import { useState } from 'react';
import type {
  InterruptAnswer,
  InterruptQuestion,
  InterruptSchema,
} from '../types';

export type WidgetState = 'pending' | 'answered';

export interface InterruptWidgetProps {
  interruptId: string;
  schema: InterruptSchema;
  state: WidgetState;
  answers?: InterruptAnswer[];
  /** Called when the user submits a form OR clicks an action button. */
  onSubmit: (interruptId: string, answers: InterruptAnswer[]) => void;
}

/** InterruptWidget renders a HITL widget (info / summary_table / form).
 *  Once `state === 'answered'`, controls disable and selections highlight. */
export function InterruptWidget(props: InterruptWidgetProps) {
  const { interruptId, schema, state, answers, onSubmit } = props;
  const isAnswered = state === 'answered';

  // Optimistic lock — disables controls between click and the
  // interrupt_resume SSE that flips state to 'answered'.
  const [submitting, setSubmitting] = useState(false);
  const locked = isAnswered || submitting;

  const handleSubmit = (ans: InterruptAnswer[]) => {
    if (locked) return;
    setSubmitting(true);
    onSubmit(interruptId, ans);
  };

  return (
    <div
      className="my-1.5 rounded-card border border-brand-accent/30 bg-brand-dark/60 px-3 py-2 text-xs font-mono"
      data-interrupt-id={interruptId}
      data-state={state}
    >
      {schema.title ? (
        <div className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-brand-accent">
          {schema.title}
        </div>
      ) : null}
      {schema.description ? (
        <div className="mb-2 text-[11px] leading-relaxed text-brand-shade2">
          {schema.description}
        </div>
      ) : null}

      {schema.output_type === 'summary_table' ? (
        <SummaryTable schema={schema} answers={answers} isAnswered={locked} onSubmit={handleSubmit} />
      ) : null}

      {schema.output_type === 'form' ? (
        <FormBody schema={schema} answers={answers} isAnswered={locked} onSubmit={handleSubmit} />
      ) : null}

      {schema.output_type === 'info' ? null : null}
    </div>
  );
}

// ─── summary_table ──────────────────────────────────────────────────────────

function SummaryTable({
  schema,
  answers,
  isAnswered,
  onSubmit,
}: {
  schema: InterruptSchema;
  answers: InterruptAnswer[] | undefined;
  isAnswered: boolean;
  onSubmit: (answers: InterruptAnswer[]) => void;
}) {
  const selected = answers?.[0]?.value ?? '';

  return (
    <div>
      {schema.rows && schema.rows.length > 0 ? (
        <div className="mb-2 grid grid-cols-[max-content_1fr] gap-x-3 gap-y-0.5 text-[11px]">
          {schema.rows.map((row, i) => (
            <div key={i} className="contents">
              <div className="text-brand-shade3">{row.label}</div>
              <div className="text-brand-light">{row.value}</div>
            </div>
          ))}
        </div>
      ) : null}
      {schema.actions && schema.actions.length > 0 ? (
        <div className="flex flex-wrap gap-1.5">
          {schema.actions.map((action, i) => {
            const isSelected = isAnswered && selected === action.value;
            const base =
              'px-2.5 py-1 text-[11px] font-mono rounded transition-colors disabled:cursor-not-allowed';
            const primary = isSelected
              ? 'bg-brand-accent text-white'
              : action.type === 'primary'
                ? 'bg-brand-accent text-white hover:bg-brand-accent/80 disabled:opacity-50'
                : 'bg-brand-shade3/20 text-brand-light hover:bg-brand-shade3/30 disabled:opacity-50';
            return (
              <button
                key={i}
                type="button"
                disabled={isAnswered}
                onClick={() =>
                  onSubmit([
                    {
                      question_id: 'action',
                      value: action.value,
                      label: action.label,
                    },
                  ])
                }
                className={`${base} ${primary} ${isSelected ? 'ring-1 ring-brand-accent' : ''}`}
              >
                {action.label}
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

// ─── form ───────────────────────────────────────────────────────────────────

function FormBody({
  schema,
  answers,
  isAnswered,
  onSubmit,
}: {
  schema: InterruptSchema;
  answers: InterruptAnswer[] | undefined;
  isAnswered: boolean;
  onSubmit: (answers: InterruptAnswer[]) => void;
}) {
  const questions = schema.questions ?? [];
  const answeredMap = new Map<string, InterruptAnswer>();
  for (const a of answers ?? []) answeredMap.set(a.question_id, a);

  const [values, setValues] = useState<Record<string, string>>(() => {
    const seed: Record<string, string> = {};
    for (const q of questions) {
      const existing = answeredMap.get(q.id);
      seed[q.id] = existing?.value ?? q.default ?? '';
    }
    return seed;
  });

  const setValue = (id: string, v: string) => setValues((prev) => ({ ...prev, [id]: v }));

  const handleSubmit = () => {
    const out: InterruptAnswer[] = questions.map((q) => {
      const v = values[q.id] ?? '';
      const label = optionLabelFor(q, v);
      return { question_id: q.id, value: v, label };
    });
    onSubmit(out);
  };

  return (
    <div className="space-y-2">
      {questions.map((q) => (
        <div key={q.id}>
          <label className="mb-1 block text-[11px] font-medium text-brand-shade2">
            {q.label}
          </label>
          {renderControl(q, values[q.id] ?? '', (v) => setValue(q.id, v), isAnswered, answeredMap.get(q.id))}
        </div>
      ))}
      <button
        type="button"
        disabled={isAnswered}
        onClick={handleSubmit}
        className="rounded bg-brand-accent px-3 py-1 text-[11px] font-mono font-semibold text-white hover:bg-brand-accent/80 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {isAnswered ? 'Submitted' : 'Submit'}
      </button>
    </div>
  );
}

function renderControl(
  q: InterruptQuestion,
  value: string,
  onChange: (v: string) => void,
  disabled: boolean,
  answered: InterruptAnswer | undefined,
) {
  const chip = (selected: boolean) =>
    `rounded px-2.5 py-1 text-[11px] font-mono transition-colors disabled:cursor-not-allowed ${
      selected
        ? 'bg-brand-accent text-white ring-1 ring-brand-accent'
        : 'bg-brand-shade3/20 text-brand-light hover:bg-brand-shade3/30'
    }`;

  if (q.type === 'text') {
    return (
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="w-full rounded border border-brand-shade3/30 bg-brand-dark px-2.5 py-1 text-[11px] font-mono text-brand-light placeholder:text-brand-shade3 focus:outline-none focus:border-brand-accent/60 disabled:opacity-60"
      />
    );
  }
  if (q.type === 'select') {
    return (
      <div className="flex flex-wrap gap-1.5">
        {(q.options ?? []).map((opt, i) => {
          const optVal = opt.value ?? opt.label;
          const isSelected = disabled
            ? (answered?.value ?? value) === optVal
            : value === optVal;
          return (
            <button
              key={i}
              type="button"
              disabled={disabled}
              onClick={() => onChange(optVal)}
              className={chip(isSelected)}
            >
              {opt.label}
            </button>
          );
        })}
      </div>
    );
  }
  // multiselect: simple newline-joined chips
  const selectedValues = new Set(value ? value.split('\n') : []);
  return (
    <div className="flex flex-wrap gap-1.5">
      {(q.options ?? []).map((opt, i) => {
        const optVal = opt.value ?? opt.label;
        const isSelected = selectedValues.has(optVal);
        return (
          <button
            key={i}
            type="button"
            disabled={disabled}
            onClick={() => {
              if (isSelected) selectedValues.delete(optVal);
              else selectedValues.add(optVal);
              onChange(Array.from(selectedValues).join('\n'));
            }}
            className={chip(isSelected)}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

function optionLabelFor(q: InterruptQuestion, value: string): string | undefined {
  if (q.type === 'text') return undefined;
  if (!q.options) return undefined;
  for (const opt of q.options) {
    const optVal = opt.value ?? opt.label;
    if (optVal === value) return opt.label;
  }
  return undefined;
}
