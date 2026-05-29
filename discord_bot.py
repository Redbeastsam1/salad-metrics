import os
import discord
from discord.ext import commands
import aiohttp
from dotenv import load_dotenv

# Load secrets from the hidden .env file
load_dotenv()
TOKEN = os.getenv("DISCORD_BOT_TOKEN")
MACHINE_NAME = os.getenv("MACHINE_NAME", "Beast")

# The local address of your Salad Metrics web server
API_BASE = "http://127.0.0.1:1000/api"

# Set up bot intents (required by Discord)
intents = discord.Intents.default()
intents.messages = True
intents.message_content = True
bot = commands.Bot(command_prefix="?", intents=intents)

@bot.event
async def on_ready():
    print("="*40)
    print(f"[INFO] Logged in as {bot.user}!")
    print(f"[INFO] Ready to monitor {MACHINE_NAME} via local API.")
    print("="*40)

@bot.command(name="status")
async def status_command(ctx):
    """Fetches live status from the local Salad Metrics API."""
    # We use aiohttp to fetch data asynchronously so the bot doesn't freeze
    async with aiohttp.ClientSession() as session:
        try:
            # Fetch data simultaneously from our three API endpoints
            async with session.get(f"{API_BASE}/data?hours=1") as r1, \
                       session.get(f"{API_BASE}/stats") as r2, \
                       session.get(f"{API_BASE}/hardware-live") as r3:

                data = await r1.json()
                stats = await r2.json()
                hw = await r3.json()

                # Parse Wallet Balance (get the most recent entry if it exists)
                current_balance = data['wallet'][-1]['current'] if data['wallet'] else 0.0

                # Parse Active Container & Rate
                active_container = "None"
                rate = 0.0
                if data['earnings']:
                    latest = data['earnings'][-1]
                    active_container = f"`{latest['container_id'][:8]}...`"
                    rate = latest['rate']

                # Build a beautiful Discord Embed
                embed = discord.Embed(
                    title=f"🥗 {MACHINE_NAME} | Live Status",
                    color=discord.Color.green()
                )
                
                # Financials Row
                embed.add_field(name="💰 Wallet Balance", value=f"${current_balance:.4f}", inline=True)
                embed.add_field(name="📈 24h Proj. Rate", value=f"${(rate * 24):.2f}/day", inline=True)
                embed.add_field(name="⏱️ Session Uptime", value=stats.get('uptime', '0h 0m'), inline=True)

                # Container Row
                embed.add_field(name="📦 Active Container", value=active_container, inline=False)

                # Hardware Row
                embed.add_field(name="🧠 CPU Load", value=f"{hw.get('cpu_load', 0):.1f}%", inline=True)
                embed.add_field(name="🎮 GPU Load", value=f"{hw.get('gpu_load', 0):.1f}% ({hw.get('gpu_temp', 0)}°C)", inline=True)
                embed.add_field(name="💾 RAM Usage", value=f"{hw.get('ram_load', 0):.1f}%", inline=True)

                await ctx.send(embed=embed)

        except Exception as e:
            await ctx.send(f"⚠️ **Error connecting to Salad Metrics API:** Make sure `server.py` is running in the background!\n`{e}`")

@bot.command(name="errors")
async def errors_command(ctx):
    """Fetches active degraded errors from the dashboard."""
    async with aiohttp.ClientSession() as session:
        try:
            async with session.get(f"{API_BASE}/errors") as r:
                errors = await r.json()

                if not errors:
                    await ctx.send(f"✅ **No active errors!** `{MACHINE_NAME}` is running smoothly.")
                    return

                embed = discord.Embed(title=f"⚠️ {MACHINE_NAME} | Active Errors", color=discord.Color.red())
                
                # Loop through the top 5 most recent errors
                for err in errors[:5]: 
                    short_msg = err['message'][:200]
                    embed.add_field(name=f"Time: {err['timestamp']}", value=f"```\n{short_msg}\n```", inline=False)

                # If there are more than 5, add a little footer note
                if len(errors) > 5:
                    embed.set_footer(text=f"...and {len(errors) - 5} more. Check the local dashboard to dismiss them.")

                await ctx.send(embed=embed)
        except Exception:
            await ctx.send("⚠️ **Error connecting to Salad Metrics API.** Make sure the web server is running.")