"use client";
import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { yaml } from "@codemirror/lang-yaml";
import { oneDark } from "@codemirror/theme-one-dark";
import { useTheme } from "@/lib/theme/context";

interface FileEditorProps {
  value: string;
  onChange: (value: string) => void;
  readOnly?: boolean;
  filename: string;
  height?: string;
}

const YAML_EXTENSIONS = new Set(["yaml", "yml"]);

function isYaml(filename: string): boolean {
  const ext = filename.split(".").pop()?.toLowerCase() ?? "";
  return YAML_EXTENSIONS.has(ext);
}

/**
 * CodeMirror 6 editor for one file of an app's volume. Only YAML gets a real
 * grammar (the only language package the console ships); everything else is
 * plain text with the same editing affordances.
 *
 * The view is rebuilt when the theme, the read-only flag or the file name
 * changes; document updates flow through a dispatch instead, so typing never
 * loses the cursor. The latest `value` and `onChange` are read through refs so
 * neither has to be an effect dependency.
 */
export function FileEditor({ value, onChange, readOnly = false, filename, height = "100%" }: FileEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  const valueRef = useRef(value);
  const externalUpdate = useRef(false);
  const { theme } = useTheme();

  useEffect(() => {
    onChangeRef.current = onChange;
    valueRef.current = value;
  });

  useEffect(() => {
    if (!containerRef.current) return;

    const updateListener = EditorView.updateListener.of((update) => {
      if (update.docChanged && !externalUpdate.current) {
        onChangeRef.current(update.state.doc.toString());
      }
    });

    const extensions = [
      basicSetup,
      EditorView.editable.of(!readOnly),
      EditorState.readOnly.of(readOnly),
      EditorView.lineWrapping,
      updateListener,
      EditorView.theme({
        "&": { height },
        ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", fontSize: "13px" },
        "&.cm-focused": { outline: "none" },
      }),
    ];
    if (isYaml(filename)) extensions.push(yaml());
    if (theme === "dark") extensions.push(oneDark);

    const view = new EditorView({
      state: EditorState.create({ doc: valueRef.current, extensions }),
      parent: containerRef.current,
    });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, [theme, readOnly, filename, height]);

  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const current = view.state.doc.toString();
    if (current === value) return;

    externalUpdate.current = true;
    view.dispatch({ changes: { from: 0, to: current.length, insert: value } });
    externalUpdate.current = false;
  }, [value]);

  return <div ref={containerRef} className="h-full overflow-auto" />;
}
