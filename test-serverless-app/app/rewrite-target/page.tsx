export default function RewriteTarget() {
  return (
    <div className="flex flex-col items-center justify-center min-h-screen bg-neutral-950 text-white p-8">
      <div className="max-w-md w-full bg-neutral-900 border border-neutral-800 rounded-2xl p-8 text-center shadow-2xl">
        <h1 className="text-3xl font-bold bg-gradient-to-r from-emerald-400 to-cyan-400 bg-clip-text text-transparent mb-4">
          Rewritten by Edge
        </h1>
        <p className="text-neutral-400">
          This page was served by Edge Proxy dynamically rewriting the URL without a redirect! 
          Check the network tab for the <code>x-proxy-custom-header</code>.
        </p>
      </div>
    </div>
  )
}
