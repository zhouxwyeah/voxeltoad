import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "../ui/button";
import { Field } from "../ui/field";
import { Input } from "../ui/input";
import { Modal } from "../ui/modal";
import { Textarea } from "../ui/textarea";
import { createPrompt, updatePrompt } from "../../lib/api";
import type { PromptTemplate } from "../../lib/types";

// PromptFormModal is the shared create/edit dialog for prompt favorites,
// used by the /prompts page (blank create, full edit) and the Trace viewer's
// "收藏" button (content prefilled from a trace row, provenance attached).
export function PromptFormModal({
  open,
  onClose,
  onSaved,
  initial,
  editRow,
}: {
  open: boolean;
  onClose: () => void;
  onSaved: () => void;
  /** Prefill for create-from-trace (content + provenance). */
  initial?: { content: string; session_id?: string; source_trace_row_id?: number };
  /** When set the modal edits this row instead of creating. */
  editRow?: PromptTemplate | null;
}) {
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [tags, setTags] = useState("");
  const [note, setNote] = useState("");
  const [pending, setPending] = useState(false);

  // Re-seed the form whenever the dialog opens or the target row changes.
  useEffect(() => {
    if (!open) return;
    setTitle(editRow?.title ?? "");
    setContent(editRow?.content ?? initial?.content ?? "");
    setTags((editRow?.tags ?? []).join(", "));
    setNote(editRow?.note ?? "");
  }, [open, editRow, initial]);

  async function onSubmit() {
    if (!title.trim() || !content.trim() || pending) return;
    setPending(true);
    const payload = {
      title: title.trim(),
      content,
      tags: tags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean),
      note,
      ...(editRow
        ? { session_id: editRow.session_id, source_trace_row_id: editRow.source_trace_row_id }
        : { session_id: initial?.session_id, source_trace_row_id: initial?.source_trace_row_id }),
    };
    try {
      if (editRow) {
        await updatePrompt(editRow.id, payload);
        toast.success("收藏已更新。");
      } else {
        await createPrompt(payload);
        toast.success("已收藏该 prompt。");
      }
      onSaved();
      onClose();
    } catch (e) {
      toast.error(String((e as Error)?.message ?? e));
    } finally {
      setPending(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editRow ? "编辑收藏" : "收藏 prompt"}
      size="lg"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" onClick={onSubmit} disabled={!title.trim() || !content.trim() || pending}>
            {pending ? "保存中…" : editRow ? "保存" : "收藏"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label="标题" required>
          <Input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="给这条 prompt 起个名字" />
        </Field>
        <Field label="内容" required hint={editRow ? undefined : "已预填当前 trace 的 messages，可编辑"}>
          <Textarea value={content} onChange={(e) => setContent(e.target.value)} rows={10} className="font-mono text-xs" />
        </Field>
        <Field label="标签" hint="逗号分隔，用于列表页筛选">
          <Input value={tags} onChange={(e) => setTags(e.target.value)} placeholder="如 system, 翻译, few-shot" />
        </Field>
        <Field label="备注">
          <Textarea value={note} onChange={(e) => setNote(e.target.value)} rows={2} placeholder="为什么值得收藏 / 使用效果" />
        </Field>
      </div>
    </Modal>
  );
}
