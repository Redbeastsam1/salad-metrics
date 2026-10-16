import os
import discord
from discord.ext import commands
import aiohttp
import psutil
from dotenv import load_dotenv

# Load secrets from the hidden .env file
load_dotenv()
TOKEN = os.getenv("DISCORD_BOT_TOKEN")
MACHINE_NAME = os.getenv("MACHINE_NAME", "Beast")

# --- AUTO-DETECT SERVER PORT ---
def get_api_base():
    """Scans running processes to find the exact port server.py is using."""
    for p in psutil.process_iter(['cmdline']):
        try:
            cmd = p.info.get('cmdline')
            # Look for our server.py script running in Python
            if cmd and any('server.py' in arg for arg in cmd):
                if '-port' in cmd:
                    port_idx = cmd.index('-port')
                    port = cmd[port_idx + 1]
                    return f"http://127.0.0.1:{port}/api"
        except (psutil.NoSuchProcess, psutil.AccessDenied):
            pass
    # Fallback to port 8000 if it can't find it
    return "http://127.0.0.1:1000/api"

API_BASE = get_api_base()
# -------------------------------

# Set up bot intents (required by Discord)
intents = discord.Intents.default()
intents.messages = True
intents.message_content = True
bot = commands.Bot(command_prefix="?", intents=intents)

@bot.event
async def on_ready():
    print("="*40)
    print(f"[INFO] Logged in as {bot.user}!")
    print(f"[INFO] Connected to Salad API at: {API_BASE}")
    print(f"[INFO] Ready to monitor {MACHINE_NAME}.")
    print("="*40)

@bot.command(name="status")
async def status_command(ctx):
    """Fetches live status from the local Salad Metrics API."""
    async with aiohttp.ClientSession() as session:
        try:
            async with session.get(f"{API_BASE}/data?hours=1") as r1, \
                       session.get(f"{API_BASE}/stats") as r2, \
                       session.get(f"{API_BASE}/hardware-live") as r3:

                data = await r1.json()
                stats = await r2.json()
                hw = await r3.json()

                current_balance = data['wallet'][-1]['current'] if data['wallet'] else 0.0

                active_container = "None"
                rate = 0.0
                if data['earnings']:
                    latest = data['earnings'][-1]
                    active_container = f"`{latest['container_id'][:8]}...`"
                    rate = latest['rate']

                #  Determine if the machine is running or idle
                machine_status = "🟢 Running" if rate > 0 else "🟡 Sitting Idle"
                embed_color = discord.Color.green() if rate > 0 else discord.Color.gold()

                embed = discord.Embed(
                    title=f"🥗 {MACHINE_NAME} | Live Status",
                    color=embed_color
                )
                
                embed.add_field(name="💰 Wallet Balance", value=f"${current_balance:.4f}", inline=True)
                embed.add_field(name="🖥️ Status", value=machine_status, inline=True)
                embed.add_field(name="⏱️ Session Uptime", value=stats.get('uptime', '0h 0m'), inline=True)
                
                embed.add_field(name="📦 Active Container", value=active_container, inline=False)
                
                embed.add_field(name="🧠 CPU Load", value=f"{hw.get('cpu_load', 0):.1f}%", inline=True)
                embed.add_field(name="🎮 GPU Load", value=f"{hw.get('gpu_load', 0):.1f}% ({hw.get('gpu_temp', 0)}°C)", inline=True)
                embed.add_field(name="💾 RAM Usage", value=f"{hw.get('ram_load', 0):.1f}%", inline=True)

                await ctx.send(embed=embed)

        except Exception as e:
            await ctx.send(f"⚠️ **Error connecting to Salad Metrics API:** Make sure `server.py` is running!\n`{e}`")

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
                
                for err in errors[:5]: 
                    short_msg = err['message'][:200]
                    embed.add_field(name=f"Time: {err['timestamp']}", value=f"```\n{short_msg}\n```", inline=False)

                if len(errors) > 5:
                    embed.set_footer(text=f"...and {len(errors) - 5} more. Check the local dashboard to dismiss them.")

                await ctx.send(embed=embed)
        except Exception:
            await ctx.send("⚠️ **Error connecting to Salad Metrics API.** Make sure the web server is running.")

if __name__ == "__main__":
    if not TOKEN:
        print("[ERROR] No Discord token found! Please check your .env file.")
    else:
        bot.run(TOKEN)