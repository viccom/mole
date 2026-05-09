import { Dialog, DialogPanel, DialogTitle } from '@headlessui/react'
import { X } from 'lucide-react'

interface ModalProps {
  title: string
  onClose: () => void
  children: React.ReactNode
  size?: 'md' | 'lg'
}

export function Modal({ title, onClose, children, size = 'md' }: ModalProps) {
  return (
    <Dialog open={true} onClose={onClose} className="relative z-[1000]">
      <div className="fixed inset-0 bg-black/50" aria-hidden="true" />
      <div className="fixed inset-0 flex items-center justify-center p-4">
        <DialogPanel className={`bg-white rounded-xl w-full max-h-[85vh] overflow-y-auto shadow-2xl ${size === 'lg' ? 'max-w-2xl' : 'max-w-md'}`}>
          <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
            <DialogTitle className="text-base font-semibold">{title}</DialogTitle>
            <button onClick={onClose} className="text-gray-400 hover:text-gray-600 p-1">
              <X className="w-5 h-5" />
            </button>
          </div>
          <div className="p-6">{children}</div>
        </DialogPanel>
      </div>
    </Dialog>
  )
}
