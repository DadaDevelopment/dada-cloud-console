"use client";
import { useEffect, useRef } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { yaml } from "@codemirror/lang-yaml";
import { oneDark } from "@codemirror/theme-one-dark";

interface YamlEditorProps {
  value: string;
  onChange: (value: string) => void;
  readOnly?: boolean;
  minHeight?: string;
}

export function YamlEditor({ value, onChange, readOnly = false, minHeight = "400px" }: YamlEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  // Track whether the last change came from outside (prop update) to avoid loops.
  const externalUpdate = useRef(false);

  useEffect(() => {
    if (!containerRef.current) return;

    const updateListener = EditorView.updateListener.of((update) => {
      if (update.docChanged && !externalUpdate.current) {
        onChange(update.state.doc.toString());
      }
    });

    const state = EditorState.create({
      doc: value,
      extensions: [
        basicSetup,
        yaml(),
        oneDark,
        EditorView.editable.of(!readOnly),
        updateListener,
        EditorView.theme({
          "&": { minHeight },
          ".cm-scroller": { fontFamily: "ui-monospace, monospace", fontSize: "13px" },
        }),
      ],
    });

    const view = new EditorView({ state, parent: containerRef.current });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // Only create the editor once; external value updates are handled below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Sync external value changes into the editor without triggering onChange.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const current = view.state.doc.toString();
    if (current === value) return;

    externalUpdate.current = true;
    view.dispatch({
      changes: { from: 0, to: current.length, insert: value },
    });
    externalUpdate.current = false;
  }, [value]);

  return (
    <div
      ref={containerRef}
      className="overflow-hidden rounded-lg border border-gray-700"
    />
  );
}
