import type {
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
} from "react";
import { forwardRef } from "react";

import { cx } from "@/components/utils";

export type FieldProps = {
  readonly children: ReactNode;
  readonly className?: string;
  readonly helperText?: ReactNode;
  readonly label: ReactNode;
};

export function Field({ children, className, helperText, label }: FieldProps) {
  return (
    <label className={cx("ui-field", className)}>
      <span className="ui-field__label">{label}</span>
      {children}
      {helperText && <span className="ui-field__helper">{helperText}</span>}
    </label>
  );
}

export type InputProps = InputHTMLAttributes<HTMLInputElement> & {
  readonly invalid?: boolean;
  readonly locked?: boolean;
};

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, invalid = false, locked = false, readOnly, ...props },
  ref,
) {
  return (
    <input
      aria-invalid={invalid || undefined}
      className={cx(
        "ui-input",
        invalid && "ui-input--invalid",
        locked && "ui-input--locked",
        className,
      )}
      readOnly={locked || readOnly}
      ref={ref}
      {...props}
    />
  );
});

export type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & {
  readonly invalid?: boolean;
};

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  function Select({ children, className, invalid = false, ...props }, ref) {
    return (
      <select
        aria-invalid={invalid || undefined}
        className={cx("ui-select", invalid && "ui-select--invalid", className)}
        ref={ref}
        {...props}
      >
        {children}
      </select>
    );
  },
);

export type LockedFieldProps = {
  readonly className?: string;
  readonly label: ReactNode;
  readonly source: ReactNode;
  readonly value: ReactNode;
};

export function LockedField({
  className,
  label,
  source,
  value,
}: LockedFieldProps) {
  return (
    <Field className={className} label={label}>
      <div className="ui-locked-field">
        <span className="ui-locked-field__value">{value}</span>
        <span className="ui-locked-field__source">{source}</span>
        <span className="ui-locked-field__lock" aria-label="locked">
          🔒
        </span>
      </div>
    </Field>
  );
}
