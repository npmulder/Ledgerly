import type { PropsWithChildren, ReactNode } from "react";

export type PageTitleProps = {
  children: ReactNode;
  id?: string;
};

export function Screen({ children }: PropsWithChildren) {
  return <main className="app-screen">{children}</main>;
}

export function SplitMain({ children }: PropsWithChildren) {
  return <div className="app-split-main">{children}</div>;
}

export function PageTitle({ children, id }: PageTitleProps) {
  return (
    <div className="app-page-title">
      <h1 className="type-page-title" id={id}>
        {children}
      </h1>
    </div>
  );
}
