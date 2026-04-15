export function Loading({ fullScreen }: { fullScreen?: boolean }) {
  return (
    <div className={fullScreen ? 'flex items-center justify-center h-screen' : 'flex items-center justify-center py-10'}>
      <div className="w-6 h-6 border-3 border-gray-200 border-t-primary rounded-full animate-spin" />
    </div>
  )
}
