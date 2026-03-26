const { S3Client, GetObjectCommand, PutObjectCommand } = require('@aws-sdk/client-s3');
const { Readable } = require('stream');

const s3 = new S3Client({ region: process.env.AWS_REGION || 'us-east-1' });
const bucket = process.env.ND_CACHE_BUCKET;

async function streamToBuffer(stream) {
  const chunks = [];
  for await (const chunk of stream) {
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

class S3CacheHandler {
  constructor(options) {
    this.options = options;
  }

  async get(key) {
    if (!bucket) return null;
    
    try {
      const command = new GetObjectCommand({
        Bucket: bucket,
        Key: `cache/${key}`,
      });
      const response = await s3.send(command);
      const buffer = await streamToBuffer(response.Body);
      return JSON.parse(buffer.toString());
    } catch (err) {
      if (err.name === 'NoSuchKey') return null;
      console.error('Cache get error:', err);
      return null;
    }
  }

  async set(key, data) {
    if (!bucket) return;
    
    try {
      const command = new PutObjectCommand({
        Bucket: bucket,
        Key: `cache/${key}`,
        Body: JSON.stringify(data),
        ContentType: 'application/json',
      });
      await s3.send(command);
    } catch (err) {
      console.error('Cache set error:', err);
    }
  }

  async revalidateTag(tag) {
    // Tags are handled by the revalidator Lambda
    console.log('Tag revalidation requested:', tag);
  }
}

module.exports = S3CacheHandler;
