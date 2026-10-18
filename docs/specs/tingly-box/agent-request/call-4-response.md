I need to look at the actual function signatures in the package to understand what types they use.

```bash
cd /home/user/tingly-dev/tingly-box-hunt && grep -rn "func.*ContentBlocks\|func.*ImageBlock\|func imageBlockToOpenAIURL\|func ConvertBeta" internal/protocol/request/ | head -30
```