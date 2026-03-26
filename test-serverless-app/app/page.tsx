import Image from "next/image";
import Link from "next/link";

export default function Home() {
  return (
    <main className="min-h-screen bg-[#0a0a0a] text-white selection:bg-cyan-500/30">
      {/* Hero Section */}
      <section className="relative overflow-hidden pt-32 pb-20 px-6">
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[800px] h-[800px] bg-gradient-to-tr from-cyan-500/20 to-purple-500/20 blur-[120px] rounded-full pointer-events-none" />
        
        <div className="max-w-5xl mx-auto text-center relative z-10">
          <h1 className="text-6xl md:text-8xl font-black tracking-tight mb-8">
            <span className="text-transparent bg-clip-text bg-gradient-to-r from-gray-100 to-gray-500">
              NextDeploy
            </span>
            <br />
            <span className="text-transparent bg-clip-text bg-gradient-to-r from-cyan-400 to-purple-500">
              Serverless Rig
            </span>
          </h1>
          <p className="text-xl md:text-2xl text-gray-400 max-w-2xl mx-auto mb-12 font-medium">
            A comprehensive verification suite to test Edge Runtimes, Middleware, Server Actions, ISR, and native Image Optimization.
          </p>
        </div>
      </section>

      {/* Grid of Tests */}
      <section className="max-w-7xl mx-auto px-6 pb-32">
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
          
          <TestCard 
            title="Image Optimization" 
            desc="Resizing both local and remote assets natively via Go Lambdas."
            href="#images"
            badge="ImgOpt"
          />
          
          <TestCard 
            title="React Server Actions" 
            desc="Executing form mutations securely on the server."
            href="/actions"
            badge="Lambda"
          />
          
          <TestCard 
            title="Edge Proxy" 
            desc="Dynamic rewrites and header injection at the CDN edge."
            href="/proxy-test?rewrite=true"
            badge="CloudFront"
          />
          
          <TestCard 
            title="ISR (Revalidation)" 
            desc="Caching static pages and selectively re-rendering them via SQS."
            href="/isr/1"
            badge="SQS"
          />

          <TestCard 
            title="Edge API Routes" 
            desc="Running low-latency serverless APIs on the Edge runtime."
            href="/api/edge"
            badge="Edge"
          />

          <TestCard 
            title="Managed Secrets" 
            desc="Injecting AWS Secrets Manager payloads into the runtime."
            href="/api/secret"
            badge="Security"
          />
          <TestCard 
            title="Proxy (Rewrites)" 
            desc="Testing Next.js proxying to an external API endpoint."
            href="/api/proxy/users/1"
            badge="Proxy"
          />
          
        </div>
      </section>

      {/* Images Section */}
      <section id="images" className="bg-[#111] border-t border-white/5 py-32 px-6">
        <div className="max-w-7xl mx-auto">
          <div className="mb-16">
            <h2 className="text-4xl font-bold mb-4">Image Optimization Engine</h2>
            <p className="text-gray-400 text-lg">Testing the Custom Go Lambda across both Local and Remote providers.</p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8">
            {/* Local Image 1 */}
            <div className="group rounded-3xl overflow-hidden bg-white/5 border border-white/10 p-4 transition duration-500 hover:bg-white/10 hover:border-white/20">
              <div className="aspect-[4/3] relative rounded-2xl overflow-hidden mb-6 bg-black">
                <Image
                  src="/images/test-image-local-1.jpg"
                  alt="High Quality Local 1"
                  fill
                  className="object-cover transition duration-700 group-hover:scale-105"
                  sizes="(max-width: 768px) 100vw, 33vw"
                />
              </div>
              <h3 className="text-xl font-semibold mb-1">Local Image (Landscape)</h3>
              <p className="text-sm text-gray-400">Served from /public/images, routed via /_next/image</p>
            </div>

            {/* Local Image 2 */}
            <div className="group rounded-3xl overflow-hidden bg-white/5 border border-white/10 p-4 transition duration-500 hover:bg-white/10 hover:border-white/20">
              <div className="aspect-[4/3] relative rounded-2xl overflow-hidden mb-6 bg-black">
                <Image
                  src="/images/test-image-local-2.jpg"
                  alt="High Quality Local 2"
                  fill
                  className="object-cover transition duration-700 group-hover:scale-105"
                  sizes="(max-width: 768px) 100vw, 33vw"
                />
              </div>
              <h3 className="text-xl font-semibold mb-1">Local Image (Nature)</h3>
              <p className="text-sm text-gray-400">High-res Unsplash downloaded to local builder.</p>
            </div>

            {/* Remote Image */}
            <div className="group rounded-3xl overflow-hidden bg-white/5 border border-white/10 p-4 transition duration-500 hover:bg-white/10 hover:border-white/20">
              <div className="aspect-[4/3] relative rounded-2xl overflow-hidden mb-6 bg-black">
                <Image
                  src="https://images.unsplash.com/photo-1682687220742-aba13b6e50ba"
                  alt="Remote Unsplash"
                  fill
                  className="object-cover transition duration-700 group-hover:scale-105"
                  sizes="(max-width: 768px) 100vw, 33vw"
                />
              </div>
              <h3 className="text-xl font-semibold mb-1">Remote Image</h3>
              <p className="text-sm text-gray-400">Fetched securely from images.unsplash.com via external domain config.</p>
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}

function TestCard({ title, desc, href, badge }: { title: string, desc: string, href: string, badge: string }) {
  return (
    <Link href={href} className="group block h-full">
      <div className="h-full bg-neutral-900/50 backdrop-blur-sm border border-neutral-800 rounded-3xl p-8 transition-all duration-300 hover:-translate-y-2 hover:bg-neutral-800/50 hover:border-neutral-700 hover:shadow-[0_8px_30px_rgb(0,0,0,0.5)]">
        <div className="flex justify-between items-start mb-6">
          <div className="w-12 h-12 rounded-2xl bg-gradient-to-br from-cyan-500 to-purple-500 p-[1px]">
            <div className="w-full h-full bg-neutral-900 rounded-2xl flex items-center justify-center">
              <span className="text-white font-bold opacity-80">TS</span>
            </div>
          </div>
          <span className="px-3 py-1 text-xs font-bold tracking-wider rounded-full bg-white/5 border border-white/10 text-gray-300">
            {badge}
          </span>
        </div>
        <h3 className="text-2xl font-bold mb-3 text-white group-hover:text-cyan-400 transition-colors">
          {title}
        </h3>
        <p className="text-gray-400 font-medium leading-relaxed">
          {desc}
        </p>
      </div>
    </Link>
  )
}
