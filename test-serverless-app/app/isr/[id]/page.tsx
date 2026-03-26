import { revalidatePath } from 'next/cache';
import Link from 'next/link';

export const revalidate = 60; // default revalidation time (60 seconds)

export async function generateStaticParams() {
  return [{ id: '1' }, { id: '2' }, { id: '3' }];
}

export default async function Page({
  params,
}: {
  params: Promise<{ id: string }>
}) {
  const { id } = await params;
  
  // Using a tag for on-demand revalidation testing
  const res = await fetch(`https://jsonplaceholder.typicode.com/posts/${id}`, {
    next: { tags: ['posts', `post-${id}`] }
  });
  const data = await res.json();

  const handleRevalidate = async () => {
    "use server";
    revalidatePath(`/isr/${id}`);
  };

  const generatedAt = new Date().toISOString();

  return (
    <main className="min-h-screen bg-[#0a0a0a] text-white p-6 lg:p-24 selection:bg-cyan-500/30">
      <div className="max-w-5xl mx-auto">
        <Link href="/" className="text-cyan-500 hover:text-cyan-400 transition-colors font-medium mb-12 inline-flex items-center gap-2">
          <span>&larr;</span> Back to Verification Suite
        </Link>
        
        <div className="mb-16">
          <div className="flex items-center gap-4 mb-6">
            <span className="px-4 py-1.5 text-sm font-bold tracking-wider rounded-full bg-cyan-500/10 border border-cyan-500/30 text-cyan-400">
              ISR Engine
            </span>
            <span className="px-4 py-1.5 text-sm font-bold tracking-wider rounded-full bg-purple-500/10 border border-purple-500/30 text-purple-400">
              SQS Revalidator
            </span>
          </div>
          <h1 className="text-5xl md:text-7xl font-black tracking-tight mb-6">
            <span className="text-transparent bg-clip-text bg-gradient-to-r from-cyan-400 to-purple-500">
              Scale to Zero
            </span>
            <br />
            <span className="text-gray-100">Cache Hits.</span>
          </h1>
          <p className="text-xl text-gray-400 max-w-2xl font-medium leading-relaxed">
            Verify that NextDeploy successfully pre-renders pages as static S3 assets, serves them instantly via CloudFront, and asynchronously rebuilds them in the background via SQS queues when revalidation is triggered.
          </p>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 mb-16">
          {/* Data Card */}
          <div className="lg:col-span-7 bg-neutral-900/40 backdrop-blur-md border border-neutral-800 rounded-3xl p-8 lg:p-10 transition duration-500 hover:bg-neutral-900/60 hover:border-neutral-700">
            <h2 className="text-2xl font-bold mb-8 text-white flex items-center gap-3">
              <div className="w-8 h-8 rounded-full bg-cyan-500/20 flex items-center justify-center">
                <div className="w-3 h-3 rounded-full bg-cyan-400 animate-pulse" />
              </div>
              Mock API Response
            </h2>
            
            <div className="bg-black/80 border border-neutral-800 rounded-2xl p-6 font-mono text-sm leading-relaxed overflow-x-auto shadow-inner">
              <div className="mb-4">
                <span className="text-purple-400 font-bold">GET</span> <span className="text-gray-300">https://jsonplaceholder.typicode.com/posts/</span><span className="text-cyan-400 font-bold">{id}</span>
              </div>
              <div className="h-px bg-neutral-800 w-full mb-4" />
              <div className="text-gray-300">
                <span className="text-pink-400">"id"</span>: <span className="text-yellow-300">{data.id}</span>,<br/>
                <span className="text-pink-400">"title"</span>: <span className="text-green-300">"{data.title}"</span>,<br/>
                <span className="text-pink-400">"body"</span>: <span className="text-green-300">"{data.body?.substring(0, 60)}..."</span>
              </div>
            </div>
            
            <p className="mt-8 text-sm text-gray-500 flex items-center gap-2">
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path></svg>
              This payload was fetched by the Lambda during build or background revalidation, not by the client.
            </p>
          </div>

          {/* Cache Status Card */}
          <div className="lg:col-span-5 bg-neutral-900/40 backdrop-blur-md border border-neutral-800 rounded-3xl p-8 lg:p-10 flex flex-col justify-between transition duration-500 hover:bg-neutral-900/60 hover:border-neutral-700">
            <div>
              <h2 className="text-2xl font-bold mb-6 text-white">Cache Signature</h2>
              <p className="text-gray-400 mb-8 text-sm leading-relaxed">
                <strong className="text-gray-200">Refresh the page</strong>. Notice the timestamp below does not change. This indicates CloudFront is intercepting the request at the edge layer and bypassing Lambda entirely.
              </p>
              
              <div className="p-5 bg-emerald-500/10 border border-emerald-500/20 rounded-2xl text-emerald-400 font-mono text-sm mb-10 shadow-[inset_0_0_20px_rgba(16,185,129,0.05)]">
                <div className="text-emerald-500/50 text-xs mb-2 tracking-widest uppercase">Generation Timestamp</div>
                <div className="font-bold">{generatedAt}</div>
              </div>
            </div>

            <div>
              <form action={handleRevalidate}>
                <button 
                  type="submit"
                  className="w-full px-6 py-4 bg-white hover:bg-gray-200 text-black font-bold rounded-xl transition-all duration-300 hover:scale-[1.02] shadow-[0_0_20px_rgba(255,255,255,0.1)] hover:shadow-[0_0_30px_rgba(255,255,255,0.2)] flex items-center justify-center gap-2"
                >
                  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg>
                  Trigger SQS Revalidation
                </button>
              </form>
              <p className="mt-5 text-xs text-center text-gray-500 leading-relaxed">
                Invokes <code className="bg-white/10 px-1 py-0.5 rounded">revalidatePath()</code> via Server Action, pushing a message to the FIFO queue for background processing.
              </p>
            </div>
          </div>
        </div>

        {/* Navigation Links */}
        <div>
          <h3 className="text-sm font-bold text-gray-500 uppercase tracking-widest mb-6">Test Other Segments</h3>
          <div className="flex flex-wrap gap-4">
            {['1', '2', '3', '4', '100'].map((pageId) => (
              <Link 
                key={pageId}
                href={`/isr/${pageId}`}
                className={`px-8 py-3 rounded-xl font-bold transition-all duration-300 border ${pageId === id ? 'bg-cyan-500/10 border-cyan-500/50 text-cyan-400 ring-1 ring-cyan-500/30' : 'bg-neutral-900/50 border-neutral-800 text-gray-400 hover:bg-neutral-800 hover:text-gray-200 shadow-sm'}`}
              >
                Post ID: {pageId}
              </Link>
            ))}
          </div>
        </div>
      </div>
    </main>
  );
}
