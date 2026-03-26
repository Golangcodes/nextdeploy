import { revalidatePath } from 'next/cache';

// Mock DB to hold server state (in a real app this would be a DB)
// For testing purposes, we'll keep it simple but avoid hydration mismatch
// by always returning the current length, but making it clear it's server-rendered.
const submissions: string[] = ['Initial test datum'];

export default async function ActionsTest() {
  
  async function submitAction(formData: FormData) {
    'use server';
    
    const entry = formData.get('entry');
    if (typeof entry === 'string' && entry.trim().length > 0) {
      submissions.push(entry);
    }
    
    // Invalidate the cache for this route to show the new entry
    revalidatePath('/actions');
  }

  // Passing the clone of the data down into the render tree guarantees Next.js
  // serializes this exact state for the client to hydrate against without mismatches.
  const currentSubmissions = [...submissions];

  return (
    <div className="flex flex-col items-center justify-center min-h-screen bg-neutral-950 text-white p-6 font-sans">
      <div className="max-w-2xl w-full bg-neutral-900 border border-neutral-800 rounded-3xl p-10 shadow-2xl">
        <h1 className="text-4xl font-extrabold mb-2 bg-gradient-to-r from-purple-400 to-pink-500 bg-clip-text text-transparent">
          React Server Actions
        </h1>
        <p className="text-neutral-400 mb-8 border-b border-neutral-800 pb-4">
          This tests Next.js Server Actions over AWS Lambda natively. No API routes required.
        </p>

        <form action={submitAction} className="flex gap-4 mb-8">
          <input 
            type="text" 
            name="entry"
            placeholder="Type something..." 
            className="flex-1 bg-neutral-950 border border-neutral-800 rounded-xl px-5 py-3 text-white focus:outline-none focus:ring-2 focus:ring-purple-500/50"
            required
          />
          <button 
            type="submit"
            className="bg-purple-600 hover:bg-purple-500 text-white font-medium rounded-xl px-6 py-3 transition-colors shadow-[0_0_15px_rgba(147,51,234,0.3)] hover:shadow-[0_0_20px_rgba(147,51,234,0.5)]"
          >
            Submit
          </button>
        </form>

        <div className="bg-neutral-950 rounded-2xl p-6 border border-neutral-800">
          <h2 className="text-xl font-semibold mb-4 text-neutral-300">Server State:</h2>
          {currentSubmissions.length === 0 ? (
             <p className="text-neutral-600 italic">No entries yet.</p>
          ) : (
            <ul className="space-y-3">
              {currentSubmissions.map((sub, idx) => (
                <li key={idx} className="flex items-center gap-3 text-neutral-300">
                  <span className="w-6 h-6 rounded-full bg-purple-500/20 text-purple-400 flex items-center justify-center text-xs font-bold shrink-0">
                    {idx + 1}
                  </span>
                  <span className="break-all">{sub}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
