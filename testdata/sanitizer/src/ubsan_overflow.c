/* Signed integer overflow when adding scores. */
#include <limits.h>
#include <stdio.h>

int add_score(int total, int score)
{
	return total + score;
}

int total_score(const int *scores, int n)
{
	int t = 0;
	for (int i = 0; i < n; i++)
		t = add_score(t, scores[i]);
	return t;
}

int main(int argc, char **argv)
{
	(void)argv;
	int scores[2] = {INT_MAX, argc};
	printf("%d\n", total_score(scores, 2));
	return 0;
}
