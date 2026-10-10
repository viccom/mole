import { useState } from 'react'
import { Dialog, DialogPanel, DialogTitle } from '@headlessui/react'

interface ConfirmDialogProps {
  title: string
  message: string
  confirmText: string
  // 支持异步操作：内部 pending 期间禁用确认按钮，防止双击重复提交
  onConfirm: () => void | Promise<void>
  onCancel: () => void
  danger?: boolean
}

export function ConfirmDialog({ title, message, confirmText, onConfirm, onCancel, danger }: ConfirmDialogProps) {
  const [pending, setPending] = useState(false)

  const handleConfirm = async () => {
    if (pending) return
    setPending(true)
    try {
      await onConfirm()
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={true} onClose={onCancel} className="relative z-[1100]">
      <div className="fixed inset-0 bg-black/50" aria-hidden="true" />
      <div className="fixed inset-0 flex items-center justify-center p-4">
        <DialogPanel className="bg-white rounded-xl w-full max-w-sm p-6 shadow-2xl">
          <DialogTitle className="text-base font-semibold mb-2">{title}</DialogTitle>
          <p className="text-sm text-gray-600 mb-6">{message}</p>
          <div className="flex justify-end gap-2">
            <button onClick={onCancel} className="px-4 py-2 text-sm border border-gray-300 rounded-lg hover:bg-gray-50">取消</button>
            <button
              onClick={handleConfirm}
              disabled={pending}
              className={`px-4 py-2 text-sm rounded-lg text-white disabled:opacity-50 ${danger ? 'bg-red-600 hover:bg-red-700' : 'bg-primary hover:bg-primary-dark'}`}
            >
              {confirmText}
            </button>
          </div>
        </DialogPanel>
      </div>
    </Dialog>
  )
}
